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
// upstream.Lookup. It records each request it receives and returns a CNAME or A
// depending on the name, so the finalize loop resolves a CNAME chain.
type captureHandler struct {
	got []*dns.Msg
}

func (h *captureHandler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	h.got = append(h.got, r.Copy())

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	switch r.Question[0].Name {
	case "bar.example.":
		m.Answer = []dns.RR{
			&dns.CNAME{
				Hdr:    dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60},
				Target: "baz.example.",
			},
		}
	case "baz.example.":
		m.Answer = []dns.RR{
			&dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP("203.0.113.10"),
			},
		}
	default:
		m.Answer = []dns.RR{
			&dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP("203.0.113.10"),
			},
		}
	}

	_ = w.WriteMsg(m)
	return dns.RcodeSuccess, nil
}

func (h *captureHandler) Name() string { return "capture" }

// multiAHandler acts like captureHandler but returns multiple A records for the final target.
type multiAHandler struct {
	got []*dns.Msg
}

func (h *multiAHandler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	h.got = append(h.got, r.Copy())

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	switch r.Question[0].Name {
	case "bar.example.":
		m.Answer = []dns.RR{
			&dns.CNAME{
				Hdr:    dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60},
				Target: "baz.example.",
			},
		}
	case "baz.example.":
		m.Answer = []dns.RR{
			&dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP("203.0.113.10"),
			},
			&dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP("203.0.113.11"),
			},
		}
	default:
		m.Answer = []dns.RR{
			&dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP("203.0.113.10"),
			},
		}
	}

	_ = w.WriteMsg(m)
	return dns.RcodeSuccess, nil
}

func (h *multiAHandler) Name() string { return "multiA" }

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
// assert that CNAME flattening actually happened.
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

func TestFinalizeFlattensCNAMEs(t *testing.T) {
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
	if len(capture.got) != 2 {
		t.Fatalf("expected two upstream lookups, got %d", len(capture.got))
	}
	assertUpstreamQuery(t, capture.got[0], "bar.example.")
	assertUpstreamQuery(t, capture.got[1], "baz.example.")

	// Verify flattening: the response should only contain A records for the original name.
	if w.msg == nil {
		t.Fatal("expected finalize to write a response")
	}
	t.Logf("final response answers: %#v", w.msg.Answer)
	if countRRType(w.msg.Answer, dns.TypeCNAME) != 0 {
		t.Fatalf("expected no CNAME records in final answer, got: %#v", w.msg.Answer)
	}
	if !allAnswersType(w.msg.Answer, dns.TypeA) {
		t.Fatalf("expected only A records in final answer, got: %#v", w.msg.Answer)
	}
	for _, rr := range w.msg.Answer {
		if rr.Header().Name != "foo.example." {
			t.Fatalf("expected answer name to be foo.example., got %q", rr.Header().Name)
		}
	}
}

func TestFinalizeFlattensMultipleARecords(t *testing.T) {
	capture := &multiAHandler{}
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

	finalize := New()
	finalize.Next = cnameHandler{}

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

	if len(capture.got) != 2 {
		t.Fatalf("expected two upstream lookups, got %d", len(capture.got))
	}
	assertUpstreamQuery(t, capture.got[0], "bar.example.")
	assertUpstreamQuery(t, capture.got[1], "baz.example.")

	if w.msg == nil {
		t.Fatal("expected finalize to write a response")
	}
	if countRRType(w.msg.Answer, dns.TypeCNAME) != 0 {
		t.Fatalf("expected no CNAME records in final answer, got: %#v", w.msg.Answer)
	}
	if countRRType(w.msg.Answer, dns.TypeA) != 2 {
		t.Fatalf("expected two A records in final answer, got: %#v", w.msg.Answer)
	}
	for _, rr := range w.msg.Answer {
		if rr.Header().Name != "foo.example." {
			t.Fatalf("expected answer name to be foo.example., got %q", rr.Header().Name)
		}
	}
}

func assertUpstreamQuery(t *testing.T, msg *dns.Msg, name string) {
	t.Helper()
	if msg == nil {
		t.Fatal("expected upstream lookup request, got none")
	}
	t.Logf("upstream lookup: qname=%s response=%t answer=%d ns=%d extra=%d", msg.Question[0].Name, msg.Response, len(msg.Answer), len(msg.Ns), len(msg.Extra))
	if msg.Response {
		t.Fatalf("expected upstream lookup to be a query, got response")
	}
	if len(msg.Answer) != 0 || len(msg.Ns) != 0 || len(msg.Extra) != 0 {
		t.Fatalf("expected upstream lookup to have empty answer/authority/additional")
	}
	if got := msg.Question[0].Name; got != name {
		t.Fatalf("expected upstream lookup to target %s, got %q", name, got)
	}
}

func countRRType(rrs []dns.RR, rrtype uint16) int {
	count := 0
	for _, rr := range rrs {
		if rr.Header().Rrtype == rrtype {
			count++
		}
	}
	return count
}

func allAnswersType(rrs []dns.RR, rrtype uint16) bool {
	if len(rrs) == 0 {
		return false
	}
	for _, rr := range rrs {
		if rr.Header().Rrtype != rrtype {
			return false
		}
	}
	return true
}
