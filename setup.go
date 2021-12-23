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
		switch len(args) {
		case 0:
			// do nothing
		case 1:
			return nil, c.ArgErr()
		case 2:
			if strings.EqualFold("max_depth", args[0]) {
				n, err := strconv.Atoi(args[1])
				if err != nil {
					return nil, err
				}
				if n <= 0 {
					return nil, fmt.Errorf("max_depth parameter must be greater than 0")
				}
				finalizePlugin.maxDepth = n
			} else {
				return nil, fmt.Errorf("unsupported parameter %s for upstream setting", args[0])
			}
		default:
			return nil, c.ArgErr()
		}
	}

	log.Debug("Successfully parsed configuration")

	return finalizePlugin, nil
}
