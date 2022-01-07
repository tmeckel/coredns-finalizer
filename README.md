# finalize

## Name

*finalize* - resolves CNAMEs to their IP address.

## Description

The plugin will try to resolve CNAMEs and only return the resulting A or AAAA
address. If no A or AAAA record can be resolved the original (first) answer will
be returned to the client.

Circular dependencies are detected and an error will be logged accordingly. In
that case the original (first) answer will be returned to the client as well.

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
finalize [max_depth MAX]
```

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

## Also See

See the [manual](https://coredns.io/manual).
