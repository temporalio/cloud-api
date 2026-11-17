# protoc-gen-nexgen

> **Status: M1–M3.** The plugin is complete, `nexusrpc/` is committed, and CI
> checks it for drift and runs nexgen on it in all four languages. The protojson
> conformance suite (M4) and the handler (§5.7) are not built yet.
> [DESIGN.md](DESIGN.md) holds the full specification, the decision log, and
> open questions.

`protoc-gen-nexgen` is a protoc / buf plugin, built on
[protoc-gen-star](https://github.com/lyft/protoc-gen-star) (PG\*) v2, that reads the Temporal Cloud API
protos in this repository and emits [nexgen](https://github.com/temporalio/nexgen)
**definition files**: JSON Schema 2020-12 documents describing every message the
`CloudService` exchanges, plus a `nexusrpc: "1.0.0"` document that exposes each
RPC as a Nexus operation.

Those definition files are then fed to `nexgen` to produce typed models,
validators, and Nexus service bindings for Go, Java, Python, and TypeScript.

```
temporal/api/cloud/**/*.proto
        │  buf generate --template buf.gen.nexgen.yaml
        ▼
protoc-gen-nexgen  ──►  nexusrpc/temporal/api/cloud/**/*.yaml   (committed)
                                │  nexgen <lang> ...
                                ▼
                     typed models + Nexus bindings (downstream)
```

## What gets generated

- **One schema file per proto package** in the closure of `CloudService`
  (e.g. `nexusrpc/temporal/api/cloud/namespace/v1/namespace.yaml`). Each holds that
  package's messages and enums under `$defs`.
- **One Nexus document** for the service package:
  `nexusrpc/temporal/api/cloud/cloudservice/v1/cloudservice.nexusrpc.yaml`. It declares
  the `CloudService` service, one operation per RPC, and the request/response
  messages in its own `$defs`.
- Cross-package references are relative-file `$ref`s
  (`../../namespace/v1/namespace.yaml#/$defs/Namespace`).

The schemas describe the **canonical protojson** encoding of each message, so a
payload produced by `protojson.Marshal` validates against the generated
definitions without a translation layer.

## Usage

```bash
make nexgen         # regenerate nexusrpc/ from the protos
make nexgen-check   # test the plugin, regenerate, and fail if nexusrpc/ has drifted (CI)
make nexgen-smoke   # run nexgen on nexusrpc/ in all four languages (CI; needs cargo)
```

Run `make nexgen` after editing a `.proto` or `nexgen.filter.yaml`, and commit
the `nexusrpc/` changes with it.

Under the hood:

```bash
(cd tools/nexgen && go build -o ../../.bin/protoc-gen-nexgen ./cmd/protoc-gen-nexgen)
buf generate --template buf.gen.nexgen.yaml
```

nexgen reads the whole `nexusrpc/` directory, because the files `$ref` each
other: `nexgen go nexusrpc --output <dir>`.

### Developing the plugin

`tools/nexgen` is its own Go module, so run these from that directory:

```bash
go test ./...                         # golden and error cases, plus unit tests
go test ./internal/module -update     # rewrite golden/ and want_error.txt after an intended change
NEXGEN=<path> go test ./internal/module  # also run every golden case through nexgen
```

Each `testdata/cases/<case>/` holds a `test.proto` (and any protos it imports;
those under `deps/` act as a dependency module), an optional `params.txt` with
the plugin parameter, an optional `filter.yaml`, and either `golden/` or, for an
`err-<name>` case, `want_error.txt`. Adding a case needs no Go changes.

### Plugin options

| Option | Default | Meaning |
| --- | --- | --- |
| `services=<fqn>[+<fqn>...]` | every service in the files to generate | Restrict the services (and therefore the type closure) that are emitted. |
| `out_format=yaml\|json` | `yaml` | Output encoding. YAML allows the `DO NOT EDIT` header comment. |
| `filter=<path>` | none | YAML file of RPCs and fields to leave out of the contract (see below). Not supported until M2. |

### Filtering

`nexgen.filter.yaml` at the repo root excludes proto elements that should not be
part of the Nexus contract. Types reachable only through excluded elements are
pruned too, and a rule that matches nothing fails the run. Today it removes the
whole async-operation surface: `GetAsyncOperation`, every `async_operation_id`
field, and every `AsyncOperation` response field. Nexus operations return only
once the underlying operation has completed.

```yaml
exclude:
  methods:     [temporal.api.cloud.cloudservice.v1.CloudService.GetAsyncOperation]
  fields:      [temporal.api.cloud.**.async_operation_id]
  field_types: [temporal.api.cloud.operation.v1.AsyncOperation]
```

See [DESIGN.md § Filtering](DESIGN.md#58-filtering-d15).

## Layout

```
tools/nexgen/
├── README.md                     # this file
├── DESIGN.md                     # spec, decisions, open questions
├── go.mod                        # separate module for the tool's own dependencies
├── cmd/protoc-gen-nexgen/main.go # pgs.Init(...).RegisterModule(module.New()).Render()
├── filter/                       # filter file loader + matcher (public: the handler reuses it)
├── internal/
│   ├── module/                   # the PG* Module: Name() + Execute() → generator artifacts
│   ├── closure/                  # mark & sweep: service refs → reachable $defs
│   ├── mapping/                  # proto descriptor → schema node (the type table)
│   ├── naming/                   # $defs keys, file paths, operation keys, reserved-word tables
│   ├── schema/                   # schema node tree with symbolic $refs
│   └── emit/                     # deterministic YAML/JSON writer
└── testdata/cases/<case>/        # test.proto + golden/ per construct
```

## Mapping at a glance

| Proto | Definition file |
| --- | --- |
| `service` / `rpc` | `services.<Name>` / `operations.<rpcName>` with `fqn: <RpcName>` |
| `message` | `$defs.<Name>`, `type: object`, open, nothing `required`; an empty message has `properties: {}` |
| field | property keyed by `json_name` (lowerCamelCase) |
| `string` / `bool` | `string` / `boolean` |
| `int32` / `uint32` / `sint32` / `fixed32` / ... | `integer` with 32-bit bounds |
| `int64` / `uint64` / ... | `string` with a decimal `pattern` (protojson encodes them as strings) |
| `float` / `double` | `number` |
| `bytes` | `string`, `contentEncoding: base64` |
| `enum` | inline `type: string` whose `description` lists the value names, so newly added values don't break older clients (nexgen has no open enum) |
| `repeated T` | `array` of `T` |
| `map<string, V>` | `object` with `additionalProperties: V` |
| `oneof` | members as ordinary optional properties, with the exclusivity noted in `description` |
| `google.protobuf.Timestamp` | `string`, `format: date-time` |
| `google.protobuf.Duration` | `string`, protojson duration `pattern` (`"1.5s"`) |
| `google.protobuf.Struct` | free-form `object` |
| `google.protobuf.Any` | open `object` requiring `@type` |
| field message type | `$ref`, with `deprecated` but no `description` (nexgen would fork the type) |
| deprecated field, message, RPC, or service | `deprecated: true` |
| property or RPC named like a reserved word | `x-java-name` / `x-py-name` / `x-ts-name: <name>_` |
| type name used in two packages | `x-go-name` / `x-py-name` / `x-ts-name: <Package><Name>` |

See [DESIGN.md § Type mapping](DESIGN.md#5-type-mapping) for the full rules.
