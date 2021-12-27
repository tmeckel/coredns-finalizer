package finalize

import (
	"context"
	"time"

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

type FinalizeLoopKey struct{}

// ServeDNS implements the plugin.Handler interface.
func (s *Finalize) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	_, ok := ctx.Value(FinalizeLoopKey{}).(int)
	if !ok {
		log.Debug("Configuring response modifier")

		ctx := context.WithValue(ctx, FinalizeLoopKey{}, 1)
		w = NewResponseModifier(ctx, s, w)
	}

	return plugin.NextOrFailure(s.Name(), s.Next, ctx, w, r)
}

// Name implements the Handler interface.
func (al *Finalize) Name() string { return "finalize" }

func recordDuration(ctx context.Context, start time.Time) {
	requestDuration.WithLabelValues(metrics.WithServer(ctx)).
		Observe(time.Since(start).Seconds())
}

type ResponseModifier struct {
	dns.ResponseWriter
	ctx    context.Context
	plugin *Finalize
}

// Returns a dns.Msg modifier that replaces CNAME on root zones with other records.
func NewResponseModifier(ctx context.Context, plugin *Finalize, w dns.ResponseWriter) *ResponseModifier {
	return &ResponseModifier{
		ResponseWriter: w,
		ctx:            ctx,
		plugin:         plugin,
	}
}

// WriteMsg records the status code and calls the
// underlying ResponseWriter's WriteMsg method.
func (r *ResponseModifier) WriteMsg(res *dns.Msg) error {
	state := request.Request{W: r.ResponseWriter, Req: res}

	log.Debugf("Finalizing CNAME for request: %+v", res)

	if res.Answer != nil && len(res.Answer) > 0 && res.Answer[0].Header().Rrtype == dns.TypeCNAME {
		requestCount.WithLabelValues(metrics.WithServer(r.ctx)).Inc()

		defer recordDuration(r.ctx, time.Now())

		// emulate hashset in go; https://emersion.fr/blog/2017/sets-in-go/
		cnameVisited := make(map[string]struct{})
		cnt := 0
		rr := res.Answer[0]
		answers := []dns.RR{
			rr,
		}
		success := true

	resolveCname:
		target := rr.(*dns.CNAME).Target
		log.Debugf("Trying to resolve CNAME [%+v] via upstream", target)

		if r.plugin.maxDepth > 0 && cnt >= r.plugin.maxDepth {
			maxDepthReachedCount.WithLabelValues(metrics.WithServer(r.ctx)).Inc()

			log.Errorf("Max depth %d reached for resolving CNAME records", r.plugin.maxDepth)
		} else if _, ok := cnameVisited[target]; ok {
			circularReferenceCount.WithLabelValues(metrics.WithServer(r.ctx)).Inc()

			log.Errorf("Detected circular reference in CNAME chain. CNAME [%s] already processed", target)
		} else {
			up, err := r.plugin.upstream.Lookup(r.ctx, state, target, state.QType())
			if err != nil {
				upstreamErrorCount.WithLabelValues(metrics.WithServer(r.ctx)).Inc()
				success = false

				log.Errorf("Failed to lookup CNAME [%+v] from upstream: [%+v]", rr, err)
			} else {
				if up.Answer == nil || len(up.Answer) == 0 {
					danglingCNameCount.WithLabelValues(metrics.WithServer(r.ctx)).Inc()
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
			res.Answer = answers
		}
	} else {
		log.Debug("Request didn't contain any answer or no CNAME")
	}

	return r.ResponseWriter.WriteMsg(res)
}

// Write is a wrapper that records the size of the message that gets written.
func (r *ResponseModifier) Write(buf []byte) (int, error) {
	n, err := r.ResponseWriter.Write(buf)

	return n, err
}

// Hijack implements dns.Hijacker. It simply wraps the underlying
// ResponseWriter's Hijack method if there is one, or returns an error.
func (r *ResponseModifier) Hijack() {
	r.ResponseWriter.Hijack()
}
