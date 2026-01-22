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

func TestFinalizeUsesQueryForUpstreamLookup(t *testing.T) {
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

	finalize := New()
	finalize.Next = cnameHandler{}

	req := new(dns.Msg)
	req.SetQuestion("foo.example.", dns.TypeA)
	req.RecursionDesired = true

	ctx := context.WithValue(context.Background(), dnsserver.Key{}, server)
	ctx = context.WithValue(ctx, dnsserver.LoopKey{}, 0)

	_, err = finalize.ServeDNS(ctx, &plugintest.ResponseWriter{}, req)
	if err != nil {
		t.Fatalf("finalize ServeDNS failed: %v", err)
	}

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
}
