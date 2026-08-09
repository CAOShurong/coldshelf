# Third-party notices

ColdShelf's executable statically links open-source Go modules. Their required license and copyright notices are included under [`third_party_licenses`](third_party_licenses).

The directory was generated from the executable dependency graph with:

```console
go run github.com/google/go-licenses/v2@v2.0.1 save ./cmd/coldshelf --save_path=third_party_licenses
```

The release SBOM records exact dependency versions. `go.mod` and `go.sum` are the source of truth for a source checkout.

All detected runtime dependencies use permissive MIT or BSD-3-Clause terms. This file is informational and does not replace the individual license texts.
