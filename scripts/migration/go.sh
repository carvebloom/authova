#!/bin/sh
set -eu
# Local cache is optional; published pinned module versions remain authoritative.
if [ -d /Users/chenhuanxiang/go/pkg/mod/cache/download ]; then
  export GOPROXY="file:///Users/chenhuanxiang/go/pkg/mod/cache/download,https://proxy.golang.org,direct"
fi
if [ -x /Users/chenhuanxiang/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.darwin-arm64/bin/go ]; then
  export GOTOOLCHAIN=local
  export PATH="/Users/chenhuanxiang/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.darwin-arm64/bin:$PATH"
  exec /Users/chenhuanxiang/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.darwin-arm64/bin/go "$@"
fi
exec go "$@"
