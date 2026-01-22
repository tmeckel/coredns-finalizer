package finalize

import (
	"context"
	"fmt"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/plugin/pkg/nonwriter"
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

type FinalizeLoopKey struct{}

// ServeDNS implements the plugin.Handler interface.
func (s *Finalize) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	req := r.Copy()
	nw := nonwriter.New(w)
	rcode, err := plugin.NextOrFailure(s.Name(), s.Next, ctx, nw, r)
	if err != nil {
		return rcode, err
	}

	r = nw.Msg
	if r == nil {
		return 1, fmt.Errorf("no answer received")
	}
	if r.Answer != nil && len(r.Answer) > 0 && r.Answer[0].Header().Rrtype == dns.TypeCNAME {
		log.Debugf("Finalizing CNAME for request: %+v", r)

		requestCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		defer recordDuration(ctx, time.Now())

		state := request.Request{W: w, Req: req}
		// emulate hashset in go; https://emersion.fr/blog/2017/sets-in-go/
		cnameVisited := make(map[string]struct{})
		cnt := 0
		rr := r.Answer[0]
		answers := []dns.RR{
			rr,
		}
		success := true

	resolveCname:
		target := rr.(*dns.CNAME).Target
		log.Debugf("Trying to resolve CNAME [%+v] via upstream", target)

		if s.maxDepth > 0 && cnt >= s.maxDepth {
			maxDepthReachedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()

			log.Errorf("Max depth %d reached for resolving CNAME records", s.maxDepth)
		} else if _, ok := cnameVisited[target]; ok {
			circularReferenceCount.WithLabelValues(metrics.WithServer(ctx)).Inc()

			log.Errorf("Detected circular reference in CNAME chain. CNAME [%s] already processed", target)
		} else {
			up, err := s.upstream.Lookup(ctx, state, target, state.QType())
			if err != nil {
				upstreamErrorCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
				success = false

				log.Errorf("Failed to lookup CNAME [%+v] from upstream: [%+v]", rr, err)
			} else {
				if up.Answer == nil || len(up.Answer) == 0 {
					danglingCNameCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
					success = false

					log.Errorf("Received no answer from upstream: [%+v]", up)
				} else {
					rr = up.Answer[0]
					switch rr.Header().Rrtype {
					case dns.TypeCNAME:
						cnt++
						cnameVisited[target] = struct{}{}
						answers = append(answers, rr)

						goto resolveCname
					case dns.TypeA:
						fallthrough
					case dns.TypeAAAA:
						answers = append(answers, up.Answer...)
					default:
						log.Errorf("Upstream server returned unsupported type [%+v] for CNAME question [%+v]", rr, up.Question[0])
						success = false
					}
				}
			}
		}

		if success {
			r.Answer = answers
		}
	} else {
		log.Debug("Request didn't contain any answer or no CNAME")
	}

	err = w.WriteMsg(r)
	if err != nil {
		return 1, err
	}

	return 0, nil
}

// Name implements the Handler interface.
func (al *Finalize) Name() string { return "finalize" }

func recordDuration(ctx context.Context, start time.Time) {
	requestDuration.WithLabelValues(metrics.WithServer(ctx)).
		Observe(time.Since(start).Seconds())
}
