package finalize

import (
	"context"
	"net"
	"testing"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	plugintest "github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
)

// captureHandler acts as the "upstream" handler inside the CoreDNS server used by
// upstream.Lookup. It records the request it receives and returns a single A record
// so finalize can append a concrete address to the CNAME chain.
type captureHandler struct {
	got *dns.Msg
}

func (h *captureHandler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	h.got = r

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	m.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("203.0.113.10"),
		},
	}

	_ = w.WriteMsg(m)
	return dns.RcodeSuccess, nil
}

func (h *captureHandler) Name() string { return "capture" }

// cnameHandler simulates the "next" plugin in the chain. It always returns a response
// containing a single CNAME so finalize is triggered and must resolve the target.
type cnameHandler struct{}

func (h cnameHandler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Answer = []dns.RR{
		&dns.CNAME{
			Hdr:    dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60},
			Target: "bar.example.",
		},
	}

	_ = w.WriteMsg(m)
	return dns.RcodeSuccess, nil
}

func (h cnameHandler) Name() string { return "cname" }

// captureResponseWriter stores the final response written by finalize so the test can
// assert that CNAME finalization actually happened.
type captureResponseWriter struct {
	*plugintest.ResponseWriter
	msg *dns.Msg
}

func newCaptureResponseWriter() *captureResponseWriter {
	return &captureResponseWriter{ResponseWriter: &plugintest.ResponseWriter{}}
}

func (w *captureResponseWriter) WriteMsg(m *dns.Msg) error {
	w.msg = m
	return nil
}

func TestFinalizeUsesQueryForUpstreamLookup(t *testing.T) {
	// Build a minimal CoreDNS server with a single handler. This server is injected into
	// the context so upstream.Lookup can call back into it as an "upstream".
	capture := &captureHandler{}
	cfg := &dnsserver.Config{
		Zone:        ".",
		ListenHosts: []string{""},
		Port:        "53",
		Plugin: []plugin.Plugin{
			func(next plugin.Handler) plugin.Handler {
				return capture
			},
		},
	}
	server, err := dnsserver.NewServer("dns://:53", []*dnsserver.Config{cfg})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Wire finalize to a stub "next" handler that always returns a CNAME response.
	finalize := New()
	finalize.Next = cnameHandler{}

	// Build a normal query and run it through finalize.
	req := new(dns.Msg)
	req.SetQuestion("foo.example.", dns.TypeA)
	req.RecursionDesired = true

	ctx := context.WithValue(context.Background(), dnsserver.Key{}, server)
	ctx = context.WithValue(ctx, dnsserver.LoopKey{}, 0)

	w := newCaptureResponseWriter()
	_, err = finalize.ServeDNS(ctx, w, req)
	if err != nil {
		t.Fatalf("finalize ServeDNS failed: %v", err)
	}

	// The upstream lookup must be a proper query (QR=0) with empty sections.
	if capture.got == nil {
		t.Fatal("expected upstream lookup request, got none")
	}
	if capture.got.Response {
		t.Fatalf("expected upstream lookup to be a query, got response")
	}
	if len(capture.got.Answer) != 0 || len(capture.got.Ns) != 0 || len(capture.got.Extra) != 0 {
		t.Fatalf("expected upstream lookup to have empty answer/authority/additional")
	}
	if got := capture.got.Question[0].Name; got != "bar.example." {
		t.Fatalf("expected upstream lookup to target bar.example., got %q", got)
	}

	// Verify finalization: the response should include the original CNAME and the resolved A.
	if w.msg == nil {
		t.Fatal("expected finalize to write a response")
	}
	if !hasRRType(w.msg.Answer, dns.TypeCNAME) || !hasRRType(w.msg.Answer, dns.TypeA) {
		t.Fatalf("expected response to contain CNAME and A records, got: %#v", w.msg.Answer)
	}
}

func hasRRType(rrs []dns.RR, rrtype uint16) bool {
	for _, rr := range rrs {
		if rr.Header().Rrtype == rrtype {
			return true
		}
	}
	return false
}
