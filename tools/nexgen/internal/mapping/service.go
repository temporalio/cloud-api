package mapping

import (
	"regexp"

	pgs "github.com/lyft/protoc-gen-star/v2"

	"github.com/temporalio/cloud-api/tools/nexgen/filter"
	"github.com/temporalio/cloud-api/tools/nexgen/internal/naming"
	"github.com/temporalio/cloud-api/tools/nexgen/internal/schema"
)

// Service is a proto service mapped to a nexusrpc service (DESIGN.md §5.1).
type Service struct {
	FQN         string
	Name        string
	Package     string
	File        string
	Description string
	Deprecated  bool
	Operations  []Operation
	Diags       []Diag
}

// Operation is one RPC.
type Operation struct {
	Key         string // lowerCamel operations key
	Wire        string // the RPC name, emitted as the operation's fqn
	Overrides   []naming.KeywordOverride
	Description string
	Deprecated  bool
	Input       *schema.Node
	Output      *schema.Node
	// At locates the RPC for diagnostics about its input and output.
	At Diag
}

// operationKey is nexgen's rule for operation names.
var operationKey = regexp.MustCompile(`^[a-z][a-zA-Z0-9]+$`)

// MapService maps s and the RPCs the filter keeps, in declaration order.
func MapService(s pgs.Service, f *filter.Filter) *Service {
	svc := &Service{
		FQN:         fqn(s),
		Name:        s.Name().String(),
		Package:     s.Package().ProtoName().String(),
		File:        s.File().Name().String(),
		Description: Comment(s),
		Deprecated:  s.Descriptor().GetOptions().GetDeprecated(),
	}
	for _, m := range s.Methods() {
		if f.ExcludesMethod(fqn(m)) {
			continue
		}
		subject := svc.Name + "." + m.Name().String()
		if m.ClientStreaming() || m.ServerStreaming() {
			svc.Diags = append(svc.Diags, Here(m, subject, "streaming RPCs can't be Nexus operations, which are unary"))
			continue
		}
		key := naming.OperationKey(m.Name().String())
		if !operationKey.MatchString(key) {
			svc.Diags = append(svc.Diags, Here(m, subject, "operation key %q must match %s, which nexgen requires; rename the RPC", key, operationKey))
			continue
		}
		op := Operation{
			Key:         key,
			Overrides:   naming.KeywordOverrides(key, false),
			Wire:        m.Name().String(),
			Description: Comment(m),
			Deprecated:  m.Descriptor().GetOptions().GetDeprecated(),
			At:          Here(m, subject, ""),
		}
		for _, io := range []struct {
			msg pgs.Message
			out **schema.Node
		}{{m.Input(), &op.Input}, {m.Output(), &op.Output}} {
			n, err := operationType(io.msg)
			if err != nil {
				svc.Diags = append(svc.Diags, Here(m, subject, "%v", err))
				continue
			}
			*io.out = n
		}
		svc.Operations = append(svc.Operations, op)
	}
	if len(svc.Operations) == 0 && len(svc.Diags) == 0 {
		svc.Diags = append(svc.Diags, Here(s, svc.Name, "no operations remain; nexgen rejects a service without operations"))
	}
	return svc
}

// operationType is a $ref to the request or response message.
// google.protobuf.Empty is inlined as an empty object, since well-known
// types have no defs.
func operationType(m pgs.Message) (*schema.Node, error) {
	if m.Package().ProtoName().String() != wktPackage {
		return &schema.Node{Ref: fqn(m)}, nil
	}
	if fqn(m) == wktPackage+".Empty" {
		return mapWKT(fqn(m))
	}
	return nil, errNotObject(fqn(m))
}

type errNotObject string

func (e errNotObject) Error() string {
	return string(e) + " can't be an operation input or output; nexgen requires a message with fields"
}
