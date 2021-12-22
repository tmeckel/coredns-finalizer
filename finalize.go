package finalize

import (
	"context"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/plugin/pkg/upstream"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

var log = clog.NewWithPlugin("finalize")

// Rewrite is plugin to rewrite requests internally before being handled.
type Finalize struct {
	Next plugin.Handler

	upstream *upstream.Upstream
	maxDepth int
}

func New() *Finalize {
	s := &Finalize{
		upstream: upstream.New(),
		maxDepth: 0,
	}

	return s
}

// ServeDNS implements the plugin.Handler interface.
func (s *Finalize) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}

	if r.Answer != nil && len(r.Answer) > 0 && r.Answer[0].Header().Rrtype == dns.TypeCNAME {
		requestCount.WithLabelValues(metrics.WithServer(ctx)).Inc()

		// emulate hashset in go; https://emersion.fr/blog/2017/sets-in-go/
		cnameVisited := make(map[string]struct{})
		cnt := 0
		rr := r.Answer[0]

	refinalizeCname:
		log.Debugf("Trying to resolve CNAME [%+v] via upstream", rr)

		if s.maxDepth > 0 && cnt >= s.maxDepth {
			maxDepthReachedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()

			log.Errorf("Max depth %d reached for resolving CNAME records", s.maxDepth)
		} else if _, ok := cnameVisited[rr.(*dns.CNAME).Target]; ok {
			circularReferenceCount.WithLabelValues(metrics.WithServer(ctx)).Inc()

			log.Errorf("Detected circular reference in CNAME chain. CNAME [%s] already processed", rr.(*dns.CNAME).Target)
		} else {
			up, err := s.upstream.Lookup(ctx, state, rr.(*dns.CNAME).Target, state.QType())
			if err != nil {
				upstreamErrorCount.WithLabelValues(metrics.WithServer(ctx)).Inc()

				log.Errorf("Failed to lookup CNAME [%+v] from upstream: [%+v]", rr, err)
			} else {
				if up.Answer == nil || len(up.Answer) == 0 {
					danglingCNameCount.WithLabelValues(metrics.WithServer(ctx)).Inc()

					log.Errorf("Received no answer from upstream: [%+v]", up)
				} else {
					rr = up.Answer[0]
					switch rr.Header().Rrtype {
					case dns.TypeCNAME:
						cnt++
						cnameVisited[up.Question[0].Name] = struct{}{}

						goto refinalizeCname
					case dns.TypeA:
						fallthrough
					case dns.TypeAAAA:
						rr.Header().Name = r.Answer[0].Header().Name
						r.Answer = []dns.RR{
							rr,
						}
					default:
						log.Errorf("Upstream server returned unsupported type [%+v] for CNAME question [%+v]", rr, up.Question[0])
					}
				}
			}
		}
	}

	if s.Next != nil {
		return plugin.NextOrFailure(state.Name(), s.Next, ctx, w, r)
	}

	err := w.WriteMsg(r)

	return 0, err
}

// Name implements the Handler interface.
func (al *Finalize) Name() string { return "finalize" }
