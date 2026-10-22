# Third-party components and notices

This inventory records the dependency versions in this source snapshot. It is
not a claim that v1.0.0 has shipped: this checkout has no v1.0.0 release SBOM.
The final v1.0.0 inventory must be checked against that release's
[`sbom.spdx.json`](https://github.com/Jonconradt/ash/releases/download/v1.0.0/sbom.spdx.json)
before approval. Go license identifiers below were checked against the
versioned module license files; the Go links open each version's license
metadata. The npm entries are from the lockfile license fields. Rust lockfile
and Python dependency evidence are noted below.

## Compatibility and approval

No GPL, AGPL, LGPL, SSPL, or other reciprocal-license component was identified
in the pinned Go modules, npm lockfile, or Rust lockfile. The identified MIT,
Apache-2.0, BSD-2-Clause, and BSD-3-Clause software licenses permit distribution
with this MIT-licensed, non-commercial project when their notices and conditions
are preserved. No known incompatible software license was found.

This is an inventory, not legal advice or maintainer approval. Maintainer
approval is **pending**. Sign-off is blocked until the release SBOM is reviewed
and the Python resolver set described below is confirmed; no approval or legal
exception is recorded in the issue comments.

## Go application and broker

The [Go module manifest](go.mod) pins 19 direct and 123 indirect requirements.
Each entry links to the exact version's license metadata.

### Apache-2.0

- [`cloud.google.com/go@v0.123.0`](https://pkg.go.dev/cloud.google.com/go@v0.123.0?tab=licenses)
- [`cloud.google.com/go/auth@v0.24.1`](https://pkg.go.dev/cloud.google.com/go/auth@v0.24.1?tab=licenses)
- [`cloud.google.com/go/compute/metadata@v0.10.0`](https://pkg.go.dev/cloud.google.com/go/compute/metadata@v0.10.0?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2@v1.47.3`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2@v1.47.3?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream@v1.7.22`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream@v1.7.22?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/config@v1.33.9`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/config@v1.33.9?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/credentials@v1.20.9`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/credentials@v1.20.9?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/feature/ec2/imds@v1.20.3`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/feature/ec2/imds@v1.20.3?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/internal/configsources@v1.5.6`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/internal/configsources@v1.5.6?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/internal/endpoints/v2@v2.8.6`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/internal/endpoints/v2@v2.8.6?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/internal/v4a@v1.5.6`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/internal/v4a@v1.5.6?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/service/bedrockruntime@v1.63.4`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/bedrockruntime@v1.63.4?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding@v1.13.21`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding@v1.13.21?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/service/internal/presigned-url@v1.14.6`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/internal/presigned-url@v1.14.6?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/service/signin@v1.10.4`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/signin@v1.10.4?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/service/sso@v1.38.4`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/sso@v1.38.4?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/service/ssooidc@v1.43.4`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/ssooidc@v1.43.4?tab=licenses)
- [`github.com/aws/aws-sdk-go-v2/service/sts@v1.51.4`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/sts@v1.51.4?tab=licenses)
- [`github.com/aws/smithy-go@v1.28.5`](https://pkg.go.dev/github.com/aws/smithy-go@v1.28.5?tab=licenses)
- [`github.com/cyberphone/json-canonicalization@v0.0.0-20241213102144-19d51d7fe467`](https://pkg.go.dev/github.com/cyberphone/json-canonicalization@v0.0.0-20241213102144-19d51d7fe467?tab=licenses)
- [`github.com/go-logr/logr@v1.4.4`](https://pkg.go.dev/github.com/go-logr/logr@v1.4.4?tab=licenses)
- [`github.com/go-logr/stdr@v1.2.2`](https://pkg.go.dev/github.com/go-logr/stdr@v1.2.2?tab=licenses)
- [`github.com/go-openapi/analysis@v1.0.1`](https://pkg.go.dev/github.com/go-openapi/analysis@v1.0.1?tab=licenses)
- [`github.com/go-openapi/errors@v0.22.9`](https://pkg.go.dev/github.com/go-openapi/errors@v0.22.9?tab=licenses)
- [`github.com/go-openapi/jsonpointer@v1.0.2`](https://pkg.go.dev/github.com/go-openapi/jsonpointer@v1.0.2?tab=licenses)
- [`github.com/go-openapi/jsonreference@v1.0.3`](https://pkg.go.dev/github.com/go-openapi/jsonreference@v1.0.3?tab=licenses)
- [`github.com/go-openapi/loads@v0.25.3`](https://pkg.go.dev/github.com/go-openapi/loads@v0.25.3?tab=licenses)
- [`github.com/go-openapi/runtime@v0.33.3`](https://pkg.go.dev/github.com/go-openapi/runtime@v0.33.3?tab=licenses)
- [`github.com/go-openapi/runtime/server-middleware@v0.33.3`](https://pkg.go.dev/github.com/go-openapi/runtime/server-middleware@v0.33.3?tab=licenses)
- [`github.com/go-openapi/spec@v1.0.1`](https://pkg.go.dev/github.com/go-openapi/spec@v1.0.1?tab=licenses)
- [`github.com/go-openapi/strfmt@v0.27.3`](https://pkg.go.dev/github.com/go-openapi/strfmt@v0.27.3?tab=licenses)
- [`github.com/go-openapi/swag/conv@v0.29.2`](https://pkg.go.dev/github.com/go-openapi/swag/conv@v0.29.2?tab=licenses)
- [`github.com/go-openapi/swag/fileutils@v0.29.2`](https://pkg.go.dev/github.com/go-openapi/swag/fileutils@v0.29.2?tab=licenses)
- [`github.com/go-openapi/swag/jsonutils@v0.29.2`](https://pkg.go.dev/github.com/go-openapi/swag/jsonutils@v0.29.2?tab=licenses)
- [`github.com/go-openapi/swag/loading@v0.29.2`](https://pkg.go.dev/github.com/go-openapi/swag/loading@v0.29.2?tab=licenses)
- [`github.com/go-openapi/swag/mangling@v0.29.2`](https://pkg.go.dev/github.com/go-openapi/swag/mangling@v0.29.2?tab=licenses)
- [`github.com/go-openapi/swag/pools@v0.29.2`](https://pkg.go.dev/github.com/go-openapi/swag/pools@v0.29.2?tab=licenses)
- [`github.com/go-openapi/swag/stringutils@v0.29.2`](https://pkg.go.dev/github.com/go-openapi/swag/stringutils@v0.29.2?tab=licenses)
- [`github.com/go-openapi/swag/typeutils@v0.29.2`](https://pkg.go.dev/github.com/go-openapi/swag/typeutils@v0.29.2?tab=licenses)
- [`github.com/go-openapi/swag/yamlutils@v0.29.2`](https://pkg.go.dev/github.com/go-openapi/swag/yamlutils@v0.29.2?tab=licenses)
- [`github.com/go-openapi/validate@v1.0.0`](https://pkg.go.dev/github.com/go-openapi/validate@v1.0.0?tab=licenses)
- [`github.com/google/certificate-transparency-go@v1.3.3`](https://pkg.go.dev/github.com/google/certificate-transparency-go@v1.3.3?tab=licenses)
- [`github.com/google/go-containerregistry@v0.22.1`](https://pkg.go.dev/github.com/google/go-containerregistry@v0.22.1?tab=licenses)
- [`github.com/google/s2a-go@v0.1.11`](https://pkg.go.dev/github.com/google/s2a-go@v0.1.11?tab=licenses)
- [`github.com/googleapis/enterprise-certificate-proxy@v0.3.23`](https://pkg.go.dev/github.com/googleapis/enterprise-certificate-proxy@v0.3.23?tab=licenses)
- [`github.com/in-toto/attestation@v1.2.0`](https://pkg.go.dev/github.com/in-toto/attestation@v1.2.0?tab=licenses)
- [`github.com/in-toto/in-toto-golang@v0.11.0`](https://pkg.go.dev/github.com/in-toto/in-toto-golang@v0.11.0?tab=licenses)
- [`github.com/oklog/ulid/v2@v2.1.2`](https://pkg.go.dev/github.com/oklog/ulid/v2@v2.1.2?tab=licenses)
- [`github.com/openai/openai-go/v2@v2.7.1`](https://pkg.go.dev/github.com/openai/openai-go/v2@v2.7.1?tab=licenses)
- [`github.com/opencontainers/go-digest@v1.0.0`](https://pkg.go.dev/github.com/opencontainers/go-digest@v1.0.0?tab=licenses)
- [`github.com/pb33f/go-yaml@v0.1.1`](https://pkg.go.dev/github.com/pb33f/go-yaml@v0.1.1?tab=licenses)
- [`github.com/pb33f/ordered-map/v2@v2.3.2`](https://pkg.go.dev/github.com/pb33f/ordered-map/v2@v2.3.2?tab=licenses)
- [`github.com/shibumi/go-pathspec@v1.3.0`](https://pkg.go.dev/github.com/shibumi/go-pathspec@v1.3.0?tab=licenses)
- [`github.com/sigstore/protobuf-specs@v0.5.2`](https://pkg.go.dev/github.com/sigstore/protobuf-specs@v0.5.2?tab=licenses)
- [`github.com/sigstore/rekor@v1.5.4`](https://pkg.go.dev/github.com/sigstore/rekor@v1.5.4?tab=licenses)
- [`github.com/sigstore/rekor-tiles/v2@v2.3.0`](https://pkg.go.dev/github.com/sigstore/rekor-tiles/v2@v2.3.0?tab=licenses)
- [`github.com/sigstore/sigstore@v1.11.0`](https://pkg.go.dev/github.com/sigstore/sigstore@v1.11.0?tab=licenses)
- [`github.com/sigstore/sigstore-go@v1.3.0`](https://pkg.go.dev/github.com/sigstore/sigstore-go@v1.3.0?tab=licenses)
- [`github.com/sigstore/timestamp-authority/v2@v2.1.4`](https://pkg.go.dev/github.com/sigstore/timestamp-authority/v2@v2.1.4?tab=licenses)
- [`github.com/theupdateframework/go-tuf/v2@v2.4.2`](https://pkg.go.dev/github.com/theupdateframework/go-tuf/v2@v2.4.2?tab=licenses)
- [`github.com/transparency-dev/formats@v0.1.1`](https://pkg.go.dev/github.com/transparency-dev/formats@v0.1.1?tab=licenses)
- [`github.com/transparency-dev/merkle@v0.0.2`](https://pkg.go.dev/github.com/transparency-dev/merkle@v0.0.2?tab=licenses)
- [`go.opentelemetry.io/auto/sdk@v1.2.1`](https://pkg.go.dev/go.opentelemetry.io/auto/sdk@v1.2.1?tab=licenses)
- [`go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.72.0`](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.72.0?tab=licenses)
- [`go.opentelemetry.io/otel@v1.47.0`](https://pkg.go.dev/go.opentelemetry.io/otel@v1.47.0?tab=licenses)
- [`go.opentelemetry.io/otel/log@v1.47.0`](https://pkg.go.dev/go.opentelemetry.io/otel/log@v1.47.0?tab=licenses)
- [`go.opentelemetry.io/otel/metric@v1.47.0`](https://pkg.go.dev/go.opentelemetry.io/otel/metric@v1.47.0?tab=licenses)
- [`go.opentelemetry.io/otel/trace@v1.47.0`](https://pkg.go.dev/go.opentelemetry.io/otel/trace@v1.47.0?tab=licenses)
- [`go.yaml.in/yaml/v3@v3.0.5`](https://pkg.go.dev/go.yaml.in/yaml/v3@v3.0.5?tab=licenses)
- [`google.golang.org/genai@v1.73.0`](https://pkg.go.dev/google.golang.org/genai@v1.73.0?tab=licenses)
- [`google.golang.org/genproto/googleapis/api@v0.0.0-20261005182115-fad411399dd8`](https://pkg.go.dev/google.golang.org/genproto/googleapis/api@v0.0.0-20261005182115-fad411399dd8?tab=licenses)
- [`google.golang.org/genproto/googleapis/rpc@v0.0.0-20261005182115-fad411399dd8`](https://pkg.go.dev/google.golang.org/genproto/googleapis/rpc@v0.0.0-20261005182115-fad411399dd8?tab=licenses)
- [`google.golang.org/grpc@v1.84.0`](https://pkg.go.dev/google.golang.org/grpc@v1.84.0?tab=licenses)
- [`k8s.io/klog/v2@v2.140.0`](https://pkg.go.dev/k8s.io/klog/v2@v2.140.0?tab=licenses)

### Apache-2.0 and MIT (contribution-dependent)

- [`github.com/modelcontextprotocol/go-sdk@v1.8.0`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.8.0?tab=licenses): its upstream license file states that contributions with relicense consent and new code/specification contributions are Apache-2.0, while contributions without consent remain MIT. The included documentation-only CC-BY-4.0 grant does not cover the compiled SDK code and its documentation is not distributed as a separate asset here.

### BSD-2-Clause

- [`github.com/digitorus/timestamp@v0.0.0-20260914073129-b4b58b92aa51`](https://pkg.go.dev/github.com/digitorus/timestamp@v0.0.0-20260914073129-b4b58b92aa51?tab=licenses)
- [`github.com/godbus/dbus/v5@v5.2.2`](https://pkg.go.dev/github.com/godbus/dbus/v5@v5.2.2?tab=licenses)
- [`github.com/gorilla/websocket@v1.5.3`](https://pkg.go.dev/github.com/gorilla/websocket@v1.5.3?tab=licenses)
- [`github.com/pkg/errors@v0.9.1`](https://pkg.go.dev/github.com/pkg/errors@v0.9.1?tab=licenses)

### BSD-3-Clause

- [`github.com/bahlo/generic-list-go@v0.2.0`](https://pkg.go.dev/github.com/bahlo/generic-list-go@v0.2.0?tab=licenses)
- [`github.com/google/go-cmp@v0.7.0`](https://pkg.go.dev/github.com/google/go-cmp@v0.7.0?tab=licenses)
- [`github.com/google/uuid@v1.6.0`](https://pkg.go.dev/github.com/google/uuid@v1.6.0?tab=licenses)
- [`github.com/googleapis/gax-go/v2@v2.26.2`](https://pkg.go.dev/github.com/googleapis/gax-go/v2@v2.26.2?tab=licenses)
- [`github.com/gorilla/css@v1.0.1`](https://pkg.go.dev/github.com/gorilla/css@v1.0.1?tab=licenses)
- [`github.com/grpc-ecosystem/grpc-gateway/v2@v2.31.0`](https://pkg.go.dev/github.com/grpc-ecosystem/grpc-gateway/v2@v2.31.0?tab=licenses)
- [`github.com/microcosm-cc/bluemonday@v1.0.27`](https://pkg.go.dev/github.com/microcosm-cc/bluemonday@v1.0.27?tab=licenses)
- [`github.com/yosida95/uritemplate/v3@v3.0.2`](https://pkg.go.dev/github.com/yosida95/uritemplate/v3@v3.0.2?tab=licenses)
- [`golang.org/x/crypto@v0.58.0`](https://pkg.go.dev/golang.org/x/crypto@v0.58.0?tab=licenses)
- [`golang.org/x/mod@v0.42.0`](https://pkg.go.dev/golang.org/x/mod@v0.42.0?tab=licenses)
- [`golang.org/x/net@v0.61.0`](https://pkg.go.dev/golang.org/x/net@v0.61.0?tab=licenses)
- [`golang.org/x/oauth2@v0.37.0`](https://pkg.go.dev/golang.org/x/oauth2@v0.37.0?tab=licenses)
- [`golang.org/x/sync@v0.24.0`](https://pkg.go.dev/golang.org/x/sync@v0.24.0?tab=licenses)
- [`golang.org/x/sys@v0.49.0`](https://pkg.go.dev/golang.org/x/sys@v0.49.0?tab=licenses)
- [`golang.org/x/term@v0.47.0`](https://pkg.go.dev/golang.org/x/term@v0.47.0?tab=licenses)
- [`golang.org/x/text@v0.43.0`](https://pkg.go.dev/golang.org/x/text@v0.43.0?tab=licenses)
- [`golang.org/x/time@v0.16.0`](https://pkg.go.dev/golang.org/x/time@v0.16.0?tab=licenses)
- [`google.golang.org/api@v0.301.0`](https://pkg.go.dev/google.golang.org/api@v0.301.0?tab=licenses)
- [`google.golang.org/protobuf@v1.36.12`](https://pkg.go.dev/google.golang.org/protobuf@v1.36.12?tab=licenses)

### MIT

- [`github.com/alecthomas/chroma/v2@v2.27.0`](https://pkg.go.dev/github.com/alecthomas/chroma/v2@v2.27.0?tab=licenses)
- [`github.com/anthropics/anthropic-sdk-go@v1.80.0`](https://pkg.go.dev/github.com/anthropics/anthropic-sdk-go@v1.80.0?tab=licenses)
- [`github.com/asaskevich/govalidator@v0.0.0-20230301143203-a9d515a09cc2`](https://pkg.go.dev/github.com/asaskevich/govalidator@v0.0.0-20230301143203-a9d515a09cc2?tab=licenses)
- [`github.com/aymanbagabas/go-osc52/v2@v2.0.1`](https://pkg.go.dev/github.com/aymanbagabas/go-osc52/v2@v2.0.1?tab=licenses)
- [`github.com/aymerick/douceur@v0.2.0`](https://pkg.go.dev/github.com/aymerick/douceur@v0.2.0?tab=licenses)
- [`github.com/buger/jsonparser@v1.6.1`](https://pkg.go.dev/github.com/buger/jsonparser@v1.6.1?tab=licenses)
- [`github.com/cenkalti/backoff/v5@v5.0.3`](https://pkg.go.dev/github.com/cenkalti/backoff/v5@v5.0.3?tab=licenses)
- [`github.com/cespare/xxhash/v2@v2.3.0`](https://pkg.go.dev/github.com/cespare/xxhash/v2@v2.3.0?tab=licenses)
- [`github.com/charmbracelet/colorprofile@v0.4.3`](https://pkg.go.dev/github.com/charmbracelet/colorprofile@v0.4.3?tab=licenses)
- [`github.com/charmbracelet/glamour@v1.0.0`](https://pkg.go.dev/github.com/charmbracelet/glamour@v1.0.0?tab=licenses)
- [`github.com/charmbracelet/lipgloss@v1.1.1-0.20250404203927-76690c660834`](https://pkg.go.dev/github.com/charmbracelet/lipgloss@v1.1.1-0.20250404203927-76690c660834?tab=licenses)
- [`github.com/charmbracelet/x/ansi@v0.11.9`](https://pkg.go.dev/github.com/charmbracelet/x/ansi@v0.11.9?tab=licenses)
- [`github.com/charmbracelet/x/cellbuf@v0.0.15`](https://pkg.go.dev/github.com/charmbracelet/x/cellbuf@v0.0.15?tab=licenses)
- [`github.com/charmbracelet/x/exp/slice@v0.1.0`](https://pkg.go.dev/github.com/charmbracelet/x/exp/slice@v0.1.0?tab=licenses)
- [`github.com/charmbracelet/x/term@v0.2.2`](https://pkg.go.dev/github.com/charmbracelet/x/term@v0.2.2?tab=licenses)
- [`github.com/clipperhouse/displaywidth@v0.11.0`](https://pkg.go.dev/github.com/clipperhouse/displaywidth@v0.11.0?tab=licenses)
- [`github.com/clipperhouse/uax29/v2@v2.7.0`](https://pkg.go.dev/github.com/clipperhouse/uax29/v2@v2.7.0?tab=licenses)
- [`github.com/cohere-ai/cohere-go/v2@v2.19.0`](https://pkg.go.dev/github.com/cohere-ai/cohere-go/v2@v2.19.0?tab=licenses)
- [`github.com/danieljoos/wincred@v1.2.3`](https://pkg.go.dev/github.com/danieljoos/wincred@v1.2.3?tab=licenses)
- [`github.com/digitorus/pkcs7@v0.0.0-20260914070511-d678ea5ea03f`](https://pkg.go.dev/github.com/digitorus/pkcs7@v0.0.0-20260914070511-d678ea5ea03f?tab=licenses)
- [`github.com/dlclark/regexp2/v2@v2.8.4`](https://pkg.go.dev/github.com/dlclark/regexp2/v2@v2.8.4?tab=licenses)
- [`github.com/felixge/httpsnoop@v1.1.0`](https://pkg.go.dev/github.com/felixge/httpsnoop@v1.1.0?tab=licenses)
- [`github.com/go-viper/mapstructure/v2@v2.5.0`](https://pkg.go.dev/github.com/go-viper/mapstructure/v2@v2.5.0?tab=licenses)
- [`github.com/google/jsonschema-go@v0.4.3`](https://pkg.go.dev/github.com/google/jsonschema-go@v0.4.3?tab=licenses)
- [`github.com/invopop/jsonschema@v0.14.0`](https://pkg.go.dev/github.com/invopop/jsonschema@v0.14.0?tab=licenses)
- [`github.com/lucasb-eyer/go-colorful@v1.4.1`](https://pkg.go.dev/github.com/lucasb-eyer/go-colorful@v1.4.1?tab=licenses)
- [`github.com/mattn/go-isatty@v0.0.24`](https://pkg.go.dev/github.com/mattn/go-isatty@v0.0.24?tab=licenses)
- [`github.com/mattn/go-runewidth@v0.0.31`](https://pkg.go.dev/github.com/mattn/go-runewidth@v0.0.31?tab=licenses)
- [`github.com/muesli/reflow@v0.3.0`](https://pkg.go.dev/github.com/muesli/reflow@v0.3.0?tab=licenses)
- [`github.com/muesli/termenv@v0.16.0`](https://pkg.go.dev/github.com/muesli/termenv@v0.16.0?tab=licenses)
- [`github.com/rivo/uniseg@v0.4.7`](https://pkg.go.dev/github.com/rivo/uniseg@v0.4.7?tab=licenses)
- [`github.com/secure-systems-lab/go-securesystemslib@v0.11.1`](https://pkg.go.dev/github.com/secure-systems-lab/go-securesystemslib@v0.11.1?tab=licenses)
- [`github.com/segmentio/asm@v1.2.1`](https://pkg.go.dev/github.com/segmentio/asm@v1.2.1?tab=licenses)
- [`github.com/segmentio/encoding@v0.5.4`](https://pkg.go.dev/github.com/segmentio/encoding@v0.5.4?tab=licenses)
- [`github.com/standard-webhooks/standard-webhooks/libraries@v0.0.1`](https://pkg.go.dev/github.com/standard-webhooks/standard-webhooks/libraries@v0.0.1?tab=licenses)
- [`github.com/tidwall/gjson@v1.20.0`](https://pkg.go.dev/github.com/tidwall/gjson@v1.20.0?tab=licenses)
- [`github.com/tidwall/match@v1.2.0`](https://pkg.go.dev/github.com/tidwall/match@v1.2.0?tab=licenses)
- [`github.com/tidwall/pretty@v1.2.2`](https://pkg.go.dev/github.com/tidwall/pretty@v1.2.2?tab=licenses)
- [`github.com/tidwall/sjson@v1.2.5`](https://pkg.go.dev/github.com/tidwall/sjson@v1.2.5?tab=licenses)
- [`github.com/xo/terminfo@v1.2.0`](https://pkg.go.dev/github.com/xo/terminfo@v1.2.0?tab=licenses)
- [`github.com/youmark/pkcs8@v0.0.0-20240726163527-a2c0da244d78`](https://pkg.go.dev/github.com/youmark/pkcs8@v0.0.0-20240726163527-a2c0da244d78?tab=licenses)
- [`github.com/yuin/goldmark@v1.8.6`](https://pkg.go.dev/github.com/yuin/goldmark@v1.8.6?tab=licenses)
- [`github.com/yuin/goldmark-emoji@v1.0.6`](https://pkg.go.dev/github.com/yuin/goldmark-emoji@v1.0.6?tab=licenses)
- [`github.com/zalando/go-keyring@v0.2.8`](https://pkg.go.dev/github.com/zalando/go-keyring@v0.2.8?tab=licenses)

## Bundled plugins and tools

| Component | Version / dependency evidence | License and distribution status |
| --- | --- | --- |
| `what_time_is_it` Go plugin | Source in `internal/app/ash_bootstrap/plugins_src/what_time_is_it`; standard library only | No third-party dependency |
| `flip_a_coin` Rust plugin | `Cargo.toml` and `Cargo.lock`; no external crates | No third-party dependency |
| `calculator` TypeScript plugin | `package-lock.json` v3; build-only [`typescript@7.0.2`](https://www.npmjs.com/package/typescript/v/7.0.2), [`@types/node@22.20.1`](https://www.npmjs.com/package/@types/node/v/22.20.1), and [`undici-types@6.21.0`](https://www.npmjs.com/package/undici-types/v/6.21.0) | TypeScript and platform packages Apache-2.0; Node types and undici-types MIT. These are build-time dependencies; the emitted plugin uses Node.js built-ins and does not bundle these packages. |
| Bundled Python tools | `internal/app/ash_bootstrap/tools/requirements.txt` pins `yfinance==0.2.65` | Apache-2.0 ([PyPI metadata](https://pypi.org/project/yfinance/0.2.65/), [upstream license](https://github.com/ranaroussi/yfinance/blob/0.2.65/LICENSE.txt)). |

The npm lockfile resolves 21 Apache-2.0 packages: `typescript@7.0.2` and the
optional `@typescript/typescript-{aix-ppc64,darwin-arm64,darwin-x64,freebsd-arm64,freebsd-x64,linux-arm,linux-arm64,linux-loong64,linux-mips64el,linux-ppc64,linux-riscv64,linux-s390x,linux-x64,netbsd-arm64,netbsd-x64,openbsd-arm64,openbsd-x64,sunos-x64,win32-arm64,win32-x64}@7.0.2`.
Its two MIT packages are `@types/node@22.20.1` and `undici-types@6.21.0`.
The platform packages are optional compiler distributions from the lockfile;
they are not shipped in the Ash release payload.

`yfinance` declares the following runtime requirements, but their transitive
versions are not locked by this repository: `pandas>=1.3.0`, `numpy>=1.16.5`,
`requests>=2.31`, `multitasking>=0.0.7`, `platformdirs>=2.0.0`,
`pytz>=2022.5`, `frozendict>=2.3.4`, `peewee>=3.16.2`,
`beautifulsoup4>=4.11.1`, `curl_cffi>=0.7`, `protobuf>=3.19.0`, and
`websockets>=13.0`. Ash installs these into a user-managed virtual environment
at install time; this unresolved resolver graph is why final compatibility
approval is pending. The upstream yfinance documentation also says Yahoo
Finance data is intended for personal use; that is a service/data-use term,
not a software license on the yfinance package, and should be separately
reviewed for Ash's intended usage.

The bundled site contains first-party HTML, CSS, JavaScript, locale data, and
`site/favicon.svg`; the plugin sources are first-party. No other third-party
assets were identified.

## Language runtime notices

Compiled Go binaries include Go standard-library code. The Go Authors license
the standard library under BSD-3-Clause; see the [Go license text](https://go.dev/LICENSE).
The `flip_a_coin` binary uses Rust standard-library code, licensed under MIT
or Apache-2.0; see the [Rust license files](https://github.com/rust-lang/rust/tree/1.99.0).
Rust plugins are bundled in Linux and FreeBSD releases, not macOS builds.
The calculator plugin requires an independently installed Node.js runtime and
does not bundle one.

## Upstream NOTICE attributions

The following dependencies carry a separate upstream `NOTICE` file in addition
to their license file. Preserve these attributions in distributed copies:

- AWS SDK for Go: Copyright 2015 Amazon.com, Inc. or its affiliates; Copyright
  2014-2015 Stripe, Inc. ([NOTICE](https://github.com/aws/aws-sdk-go-v2/blob/v1.47.3/NOTICE.txt)).
- AWS Smithy Go: Copyright Amazon.com, Inc. or its affiliates
  ([NOTICE](https://github.com/aws/smithy-go/blob/v1.28.5/NOTICE)).
- go-openapi `jsonpointer`, `jsonreference`, and `runtime`: Copyright
  2015-2025 go-swagger maintainers; notices also attribute upstream
  `jsonpointer`, `jsonreference`, and `denco` code to their original authors.
  See each package's linked license metadata above.
- `pb33f/go-yaml`: Apache-2.0 for the project, with the files listed in its
  [NOTICE](https://github.com/pb33f/go-yaml/blob/v0.1.1/NOTICE) ported from
  libyaml and separately attributed under MIT to Kirill Simonov (Copyright
  2006-2010).
- The Update Framework Go client: Copyright 2024 The Update Framework Authors;
  the notice also attributes Apache Software Foundation material
  ([NOTICE](https://github.com/theupdateframework/go-tuf/blob/v2.4.2/NOTICE)).
- `go.yaml.in/yaml/v3`: Copyright 2011-2016 Canonical Ltd.
  ([NOTICE](https://github.com/go-yaml/yaml/blob/v3.0.5/NOTICE)).
- gRPC-Go: Copyright 2014 gRPC authors
  ([NOTICE](https://github.com/grpc/grpc-go/blob/v1.84.0/NOTICE.txt)).

All distributed software is subject to its upstream license terms. The exact
versioned Go module license files, npm license metadata, and yfinance license
are linked above. For a later release, verify that the license files and
required full license texts are retained in its artifacts alongside this
inventory; the root [`LICENSE`](LICENSE) covers Ash's own code and does not
replace those third-party terms.

## Release artifact coverage and review record

Release archives and installer packages must carry both `LICENSE` and this
notice file. The release workflow publishes the notice beside the per-release
SPDX SBOM and checksums. The downloadable website archive contains the site
only, not the Ash application or its dependencies.

| Review item | Status |
| --- | --- |
| Current source manifests and module license files inspected | Complete for the pinned Go and npm entries; Rust plugin has no external crates |
| v1.0.0 artifact SBOM compared with this inventory | Pending; no v1.0.0 SBOM/release artifact is present in this checkout |
| Python transitive dependency licenses | Pending; repository pins `yfinance` only, not its resolver graph |
| Required release attribution staged for v1.0.0 artifacts | Pending until v1.0.0 artifacts are rebuilt and inspected |
| Maintainer/legal approval | Pending; no approval has been recorded |
