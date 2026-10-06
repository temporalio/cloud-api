// Command protoc-gen-nexgen is a protoc/buf plugin that emits nexgen
// definition files for the services in the files it is asked to generate.
// See tools/nexgen/DESIGN.md.
package main

import (
	pgs "github.com/lyft/protoc-gen-star/v2"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/temporalio/cloud-api/tools/nexgen/internal/module"
)

func main() {
	optional := uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
	pgs.Init(
		pgs.DebugEnv("DEBUG_NEXGEN"),
		pgs.SupportedFeatures(&optional),
	).RegisterModule(module.New()).Render()
}
