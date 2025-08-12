package wasmcloud.access_test

import data.wasmcloud.access

test_allow_provider if {
  access.allow with input as {"requestId": "abc", "kind": "startProvider", "request": {"imageRef": "ghcr.io/wasmcloud/http-server:0.23"}}
}
