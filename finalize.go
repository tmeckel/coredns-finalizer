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

	upstream     *upstream.Upstream
	maxDepth     int
	forceResolve bool
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
	qname := ""
	qtype := uint16(0)
	if r != nil && len(r.Question) > 0 {
		qname = r.Question[0].Name
		qtype = r.Question[0].Qtype
	}
	log.Debugf("ServeDNS query name=%s type=%s force_resolve=%t max_depth=%d", qname, dns.Type(qtype).String(), s.forceResolve, s.maxDepth)

	req := r.Copy()
	origName := ""
	if len(req.Question) > 0 {
		origName = req.Question[0].Name
	}

	nw := nonwriter.New(w)
	rcode, err := plugin.NextOrFailure(s.Name(), s.Next, ctx, nw, r)
	if err != nil {
		return rcode, err
	}

	r = nw.Msg
	if r == nil {
		return 1, fmt.Errorf("no answer received")
	}
	log.Debugf("Upstream response rcode=%s answers=%d", dns.RcodeToString[r.Rcode], len(r.Answer))

	if len(r.Question) > 0 && r.Question[0].Qtype == dns.TypeCNAME {
		log.Debug("Request is a CNAME type question, skipping")
		if err := w.WriteMsg(r); err != nil {
			return 1, err
		}
		return 0, nil
	}

	if len(r.Answer) > 0 && r.Answer[0].Header().Rrtype == dns.TypeCNAME {
		log.Debugf("Finalizing CNAME for request: %+v", r)

		requestCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		defer recordDuration(ctx, time.Now())

		state := request.Request{W: w, Req: req}
		// emulate hashset in go; https://emersion.fr/blog/2017/sets-in-go/
		cnameVisited := make(map[string]struct{})
		cnt := 0
		rr := r.Answer[0]
		answers := []dns.RR{}
		success := true

	resolveCname:
		target := rr.(*dns.CNAME).Target
		if origName == "" {
			origName = target
		}
		log.Debugf("Trying to resolve CNAME target=%s type=%s", target, dns.Type(state.QType()).String())

		if s.maxDepth > 0 && cnt >= s.maxDepth {
			maxDepthReachedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()

			log.Errorf("Max depth %d reached for resolving CNAME records", s.maxDepth)
		} else if _, ok := cnameVisited[target]; ok {
			circularReferenceCount.WithLabelValues(metrics.WithServer(ctx)).Inc()

			log.Errorf("Detected circular reference in CNAME chain. CNAME [%s] already processed", target)
		} else {
			terminal := terminalAnswers(r.Answer)
			if len(terminal) > 0 && !s.forceResolve {
				log.Debugf("Using terminal A/AAAA from original answer count=%d", len(terminal))
				answers = append(answers, flattenAnswers(terminal, origName)...)
			} else {
				if len(terminal) > 0 && s.forceResolve {
					log.Debugf("force_resolve enabled; ignoring terminal A/AAAA in original answer count=%d", len(terminal))
				}
				up, err := s.upstream.Lookup(ctx, state, target, state.QType())
				if err != nil {
					upstreamErrorCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
					success = false

					log.Errorf("Failed to lookup CNAME [%+v] from upstream: [%+v]", rr, err)
				} else {
					ansCount := 0
					if up.Answer != nil {
						ansCount = len(up.Answer)
					}
					log.Debugf("Lookup response rcode=%s answers=%d", dns.RcodeToString[up.Rcode], ansCount)

					if len(up.Answer) == 0 {
						danglingCNameCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
						success = false

						log.Errorf("Received no answer from upstream: [%+v]", up)
					} else {
						rr = up.Answer[0]
						switch rr.Header().Rrtype {
						case dns.TypeCNAME:
							cnt++
							cnameVisited[target] = struct{}{}

							goto resolveCname
						case dns.TypeA:
							fallthrough
						case dns.TypeAAAA:
							answers = append(answers, flattenAnswers(up.Answer, origName)...)
						default:
							log.Errorf("Upstream server returned unsupported type [%+v] for CNAME question [%+v]", rr, up.Question[0])
							success = false
						}
					}
				}
			}
		}

		if success && len(answers) > 0 {
			log.Debugf("Finalized answer count=%d name=%s", len(answers), origName)
			r.Answer = answers
		} else if !success {
			log.Debugf("Finalization failed; returning original answer")
		} else {
			log.Debugf("Finalization produced no answers; returning original answer")
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

func terminalAnswers(rrs []dns.RR) []dns.RR {
	terminal := make([]dns.RR, 0, len(rrs))
	for _, rr := range rrs {
		switch rr.Header().Rrtype {
		case dns.TypeA, dns.TypeAAAA:
			terminal = append(terminal, rr)
		}
	}
	return terminal
}

func flattenAnswers(rrs []dns.RR, name string) []dns.RR {
	flattened := make([]dns.RR, 0, len(rrs))
	for _, rr := range rrs {
		switch rr.Header().Rrtype {
		case dns.TypeA, dns.TypeAAAA:
			copied := dns.Copy(rr)
			copied.Header().Name = name
			flattened = append(flattened, copied)
		}
	}
	return flattened
}

func recordDuration(ctx context.Context, start time.Time) {
	requestDuration.WithLabelValues(metrics.WithServer(ctx)).
		Observe(time.Since(start).Seconds())
}
