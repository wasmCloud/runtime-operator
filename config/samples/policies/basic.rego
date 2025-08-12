package wasmcloud.access

import rego.v1

allow if {
  input.kind in ["performInvocation", "startComponent"]
}

allow if {
  input.kind == "startProvider"
  startswith(input.request.imageRef, "ghcr.io/wasmcloud/http-server:0.23")
}
