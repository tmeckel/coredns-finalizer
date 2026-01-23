package finalize

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
)

// init registers this plugin.
func init() { plugin.Register("finalize", setup) }

func setup(c *caddy.Controller) error {
	finalize, err := parse(c)
	if err != nil {
		return plugin.Error("finalize", err)
	}
	// Add the Plugin to CoreDNS, so Servers can use it in their plugin chain.
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		finalize.Next = next

		return finalize
	})

	log.Debug("Added plugin to server")

	return nil
}

func parse(c *caddy.Controller) (*Finalize, error) {
	finalizePlugin := New()
	for c.Next() {
		args := c.RemainingArgs()
		if len(args) == 0 {
			continue
		}
		for i := 0; i < len(args); {
			switch strings.ToLower(args[i]) {
			case "force_resolve":
				finalizePlugin.forceResolve = true
				i++
			case "max_depth":
				if i+1 >= len(args) {
					return nil, c.ArgErr()
				}
				n, err := strconv.Atoi(args[i+1])
				if err != nil {
					return nil, err
				}
				if n <= 0 {
					return nil, fmt.Errorf("max_depth parameter must be greater than 0")
				}
				finalizePlugin.maxDepth = n
				i += 2
			default:
				return nil, fmt.Errorf("unsupported parameter %s for finalize setting", args[i])
			}
		}
	}

	log.Debug("Successfully parsed configuration")

	return finalizePlugin, nil
}
