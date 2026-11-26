package module_test

import (
	"bytes"
	"context"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/temporalio/cloud-api/tools/nexgen/internal/module"
)

var update = flag.Bool("update", false, "rewrite golden files")

const casesDir = "../../testdata/cases"

// TestCases runs every testdata/cases/<case> through the real PG* pipeline.
// A case holds test.proto (plus any .proto files it imports, relative to the
// case directory; files under deps/ stand in for a dependency module and are
// not generated) and an optional params.txt with the plugin parameter. The
// plugin runs with the case directory as its working directory, so a filter
// option names a file there. An err-<name> case expects the error in
// want_error.txt; any other case expects the files in golden/. Adding a case
// needs no Go changes.
//
// When NEXGEN names a nexgen binary, every golden case is also run through
// nexgen for each target language, which fails the case if nexgen rejects it.
func TestCases(t *testing.T) {
	root, err := filepath.Abs(casesDir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		wantErr := strings.HasPrefix(e.Name(), "err-")
		t.Run(e.Name(), func(t *testing.T) { runCase(t, dir, wantErr) })
	}
}

func runCase(t *testing.T, dir string, wantErr bool) {
	t.Chdir(dir)
	params := ""
	if b, err := os.ReadFile(filepath.Join(dir, "params.txt")); err == nil {
		params = strings.TrimSpace(string(b))
	}
	resp := generate(t, dir, params)

	if wantErr {
		if resp.Error == nil {
			t.Fatalf("expected an error, got %d files", len(resp.GetFile()))
		}
		if len(resp.GetFile()) > 0 {
			t.Errorf("expected no files alongside the error, got %d", len(resp.GetFile()))
		}
		checkGolden(t, filepath.Join(dir, "want_error.txt"), []byte(resp.GetError()+"\n"))
		return
	}
	if resp.Error != nil {
		t.Fatalf("plugin error:\n%s", resp.GetError())
	}

	goldenDir := filepath.Join(dir, "golden")
	got := map[string][]byte{}
	for _, f := range resp.GetFile() {
		got[filepath.FromSlash(f.GetName())] = []byte(f.GetContent())
	}
	if *update {
		if err := os.RemoveAll(goldenDir); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range got {
		checkGolden(t, filepath.Join(goldenDir, name), content)
	}
	// Every golden file must have been generated.
	_ = filepath.WalkDir(goldenDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(goldenDir, p)
		if _, ok := got[rel]; !ok {
			t.Errorf("golden file %s was not generated", rel)
		}
		return nil
	})
	if bin := os.Getenv("NEXGEN"); bin != "" && !strings.Contains(params, "out_format=json") {
		acceptNexgen(t, bin, goldenDir)
	}
}

// acceptNexgen runs nexgen on a golden directory for every target language.
func acceptNexgen(t *testing.T, bin, goldenDir string) {
	t.Helper()
	for _, lang := range []string{"go", "java", "python", "typescript"} {
		out := filepath.Join(t.TempDir(), "gen")
		args := []string{lang, goldenDir, "--output", out}
		if lang == "java" {
			args = append(args, "--package-name", "com.example.gen")
		}
		if b, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
			t.Errorf("nexgen %s rejected the output: %v\n%s", lang, err, b)
		}
	}
}

func checkGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./... -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from output (run go test ./... -update to accept)\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

// generate compiles dir/test.proto and runs the plugin on it in-process.
func generate(t *testing.T, dir, params string) *pluginpb.CodeGeneratorResponse {
	t.Helper()
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{dir},
		}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	files, err := compiler.Compile(context.Background(), "test.proto")
	if err != nil {
		t.Fatalf("compiling test.proto: %v", err)
	}

	req := &pluginpb.CodeGeneratorRequest{Parameter: proto.String(params)}
	seen := map[string]bool{}
	var add func(fd protoreflect.FileDescriptor)
	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		seen[fd.Path()] = true
		imports := fd.Imports()
		for i := 0; i < imports.Len(); i++ {
			add(imports.Get(i).FileDescriptor)
		}
		req.ProtoFile = append(req.ProtoFile, toProto(fd))
		if p := fd.Path(); !strings.HasPrefix(p, "google/") && !strings.HasPrefix(p, "deps/") {
			req.FileToGenerate = append(req.FileToGenerate, p)
		}
	}
	for _, f := range files {
		add(f)
	}
	in, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	optional := uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
	pgs.Init(
		pgs.ProtocInput(bytes.NewReader(in)),
		pgs.ProtocOutput(&out),
		pgs.SupportedFeatures(&optional),
	).RegisterModule(module.New()).Render()

	resp := &pluginpb.CodeGeneratorResponse{}
	if err := proto.Unmarshal(out.Bytes(), resp); err != nil {
		t.Fatal(err)
	}
	sort.Slice(resp.File, func(i, j int) bool { return resp.File[i].GetName() < resp.File[j].GetName() })
	return resp
}

// toProto converts fd the way protoc hands files to a plugin, including
// json_name on every field.
func toProto(fd protoreflect.FileDescriptor) *descriptorpb.FileDescriptorProto {
	var fdp *descriptorpb.FileDescriptorProto
	if r, ok := fd.(linker.Result); ok {
		fdp = proto.Clone(r.FileDescriptorProto()).(*descriptorpb.FileDescriptorProto)
	} else {
		fdp = protodesc.ToFileDescriptorProto(fd)
	}
	var setJSON func(msgs []*descriptorpb.DescriptorProto, descs protoreflect.MessageDescriptors)
	setJSON = func(msgs []*descriptorpb.DescriptorProto, descs protoreflect.MessageDescriptors) {
		for i, m := range msgs {
			md := descs.Get(i)
			for j, f := range m.Field {
				f.JsonName = proto.String(md.Fields().Get(j).JSONName())
			}
			setJSON(m.NestedType, md.Messages())
		}
	}
	setJSON(fdp.MessageType, fd.Messages())
	return fdp
}
