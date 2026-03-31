# finalize

## Name

*finalize* - resolves CNAMEs to their IP address.

## Description

The plugin will try to resolve CNAMEs and only return the resulting A or AAAA
address. If no A or AAAA record can be resolved the original (first) answer will
be returned to the client.

If the original answer already includes terminal A or AAAA records, finalize will
reuse those without additional upstream lookups unless `force_resolve` is enabled.

Circular dependencies are detected and an error will be logged accordingly. In
that case the original (first) answer will be returned to the client as well.

### TTL Behavior

The finalize plugin uses the **minimum TTL** encountered across the entire CNAME chain
for the final flattened response. This includes:

- TTL of the initial CNAME record
- TTL of any intermediate CNAME records in the chain
- TTL of the terminal A/AAAA record(s)

Using the minimum TTL ensures that clients and caches respect the shortest time-to-live
value in the chain, which is critical for scenarios involving fast DNS-based failover.

For example, given the following DNS setup:

```
example.com    CNAME 60s  -> primary.example.com
primary.example.com  A   3600s -> 192.0.2.1
```

When `example.com` is queried, the finalize plugin will return:

```
example.com    A 60s -> 192.0.2.1
```

The response uses TTL=60s (from the CNAME), not TTL=3600s (from the A record). This allows
quick failover when the CNAME is repointed to a backup server, without being blocked by
long-cached A record TTLs.

## Compilation

A simple way to consume this plugin, is by adding the following on [plugin.cfg](https://github.com/coredns/coredns/blob/master/plugin.cfg) __right after the `cache` plugin__,
and recompile it as [detailed on coredns.io](https://coredns.io/2017/07/25/compile-time-enabling-or-disabling-plugins/#build-with-compile-time-configuration-file).

```txt
# right after cache:cache
finalize:github.com/tmeckel/coredns-finalizer
```

After this you can compile coredns by:

```sh
go generate
go build
```

Or you can instead use make:

```sh
make
```

## Syntax

```txt
finalize [force_resolve] [max_depth MAX]
```

* `force_resolve` forces CNAME targets to be resolved via upstream lookups even
    if the original answer already contains terminal A or AAAA records. The
    `max_depth` limit is still honored.

* `max_depth` **MAX** to limit the maximum calls to resolve a CNAME chain to the
    final A or AAAA record, a value `> 0` can be specified.

    If the maximum depth
    is reached and no A or AAAA record could be found, the the original (first)
    answer, containing the CNAME, will be returned to the client.

## Metrics

If monitoring is enabled (via the *prometheus* directive) the following metrics are exported:

* `coredns_finalize_request_count_total{server}` - query count to the *finalize* plugin.

* `coredns_finalize_circular_reference_count_total{server}` - count of detected circular references.

* `coredns_finalize_dangling_cname_count_total{server}` - count of CNAMEs that couldn't be resolved.

* `coredns_finalize_maxdepth_reached_count_total{server}` - count of incidents when max depth is reached while trying to resolve a CNAME.

* `coredns_finalize_maxdepth_upstream_error_count_total{server}` - count of upstream errors received.

* `coredns_finalize_request_duration_seconds{server}` - duration per CNAME resolve.

The `server` label indicated which server handled the request.

## Ready

This plugin will be immediately ready and thus does not report it's status.

## Examples

In this configuration, we forward all queries to 9.9.9.9 and resolve CNAMEs.

```corefile
. {
  forward . 9.9.9.9
  finalize
}
```

In this configuration, we forward all queries to 9.9.9.9 and resolve CNAMEs with a maximum search depth of `1`:

```corefile
. {
  forward . 9.9.9.9
  finalize max_depth 1
}
```

In this configuration, we always resolve the CNAME chain via upstream lookups and
limit the maximum search depth to `2`:

```corefile
. {
  forward . 9.9.9.9
  finalize force_resolve max_depth 2
}
```

## Also See

See the [manual](https://coredns.io/manual).
