$(VERBOSE).SILENT:
############################# Main targets #############################
ci-build: install proto nexgen-check nexgen-smoke

# Install dependencies.
install: buf-install grpc-install openapiv2-install

# Run all linters and compile proto files.
proto: grpc
########################################################################

##### Variables ######
ifndef GOPATH
GOPATH := $(shell go env GOPATH)
endif

GOBIN := $(if $(shell go env GOBIN),$(shell go env GOBIN),$(GOPATH)/bin)
SHELL := PATH=$(GOBIN):$(PATH) /bin/sh

COLOR := "\e[1;36m%s\e[0m\n"

PROTO_OUT := .gen
$(PROTO_OUT):
	mkdir $(PROTO_OUT)

##### Compile proto files for go #####
grpc: buf-lint buf-breaking go-grpc

go-grpc: clean $(PROTO_OUT)
	printf $(COLOR) "Compile for go-gRPC..."
	buf generate --output $(PROTO_OUT)

##### Nexus definitions (tools/nexgen) #####
# protoc-gen-nexgen projects the protos into nexgen definition files under
# nexusrpc/, which are committed. See tools/nexgen/README.md.
NEXGEN_OUT := nexusrpc
# nexgen has no release yet, so it is pinned to a commit and built with cargo.
NEXGEN_REPO := https://github.com/temporalio/nexgen
NEXGEN_REV := b80f08d56f09aa7ac0909dbfa2e02aa8a21ccbda
NEXGEN := .bin/nexgen-$(NEXGEN_REV)/bin/nexgen

nexgen-build:
	printf $(COLOR) "Build protoc-gen-nexgen..."
	cd tools/nexgen && go build -o ../../.bin/protoc-gen-nexgen ./cmd/protoc-gen-nexgen

nexgen-test:
	printf $(COLOR) "Test protoc-gen-nexgen..."
	cd tools/nexgen && go test ./...

# Regenerate nexusrpc/. It is removed first so a renamed package leaves no
# stale file behind.
nexgen: nexgen-build
	printf $(COLOR) "Generate nexgen definitions..."
	rm -rf $(NEXGEN_OUT)
	buf generate --template buf.gen.nexgen.yaml

# One line: GNU make with this Makefile's SHELL doesn't keep backslash-newline
# continuations together.
nexgen-check: nexgen-test nexgen
	@[ -z "$$(git status --porcelain -- $(NEXGEN_OUT))" ] || { echo "$(NEXGEN_OUT)/ is out of date; run 'make nexgen' and commit the result"; git status --short -- $(NEXGEN_OUT); git --no-pager diff -- $(NEXGEN_OUT); exit 1; }

$(NEXGEN):
	printf $(COLOR) "Install nexgen $(NEXGEN_REV)..."
	CARGO_NET_GIT_FETCH_WITH_CLI=true cargo install --locked --git $(NEXGEN_REPO) --rev $(NEXGEN_REV) --root .bin/nexgen-$(NEXGEN_REV) nexgen

nexgen-install: $(NEXGEN)

# Check that nexgen accepts the definitions in every target language.
nexgen-smoke: $(NEXGEN)
	printf $(COLOR) "Run nexgen on $(NEXGEN_OUT)/..."
	rm -rf $(PROTO_OUT)/nexgen-smoke
	$(NEXGEN) go $(NEXGEN_OUT) --output $(PROTO_OUT)/nexgen-smoke/go
	$(NEXGEN) python $(NEXGEN_OUT) --output $(PROTO_OUT)/nexgen-smoke/python
	$(NEXGEN) typescript $(NEXGEN_OUT) --output $(PROTO_OUT)/nexgen-smoke/typescript
	$(NEXGEN) java $(NEXGEN_OUT) --output $(PROTO_OUT)/nexgen-smoke/java/cloud --package-name io.temporal.api.cloud

##### Plugins & tools #####
buf-install:
	printf $(COLOR) "Install/update buf..."
	go install github.com/bufbuild/buf/cmd/buf@v1.25.1

grpc-install:
	printf $(COLOR) "Install/update go and grpc protoc gen ..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.31
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3

openapiv2-install:
	printf $(COLOR) "Install/update openapiv2 protoc gen..."
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.26.3

##### Linters #####
buf-lint:
	printf $(COLOR) "Run buf linter..."
	buf lint

buf-breaking:
	@printf $(COLOR) "Run buf breaking changes check against main branch..."
	buf breaking --against 'https://github.com/temporalio/api-cloud.git#branch=main'

##### Clean #####
clean:
	printf $(COLOR) "Delete generated go files..."
	rm -rf $(PROTO_OUT)
