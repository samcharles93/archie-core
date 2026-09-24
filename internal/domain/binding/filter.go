package binding

import (
	"fmt"
	"strings"

	"cel.dev/cel-go/cel"

	"github.com/samcharles93/archie-core/internal/domain/eda/expr"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
)

// Filter is a compiled binding filter: a CEL expression over the mapping's
// named parameters, each declared as a variable of its field type
// (`severity in ["high", "critical"]`). A nil Filter admits every event.
type Filter struct {
	prg cel.Program
}

// CompileFilter checks src against the mapping's fields and compiles it. An
// empty src is no filter. A reference to a parameter the mapping lacks, a type
// error, or a non-boolean result is refused.
func CompileFilter(src string, fields []mapping.Field) (*Filter, error) {
	if strings.TrimSpace(src) == "" {
		return nil, nil
	}
	opts := make([]cel.EnvOption, 0, len(fields))
	for _, f := range fields {
		opts = append(opts, cel.Variable(f.Name, celType(f.Type)))
	}
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, fmt.Errorf("binding: filter: %w", err)
	}
	ast, issues := env.Compile(src)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("binding: filter: %w", issues.Err())
	}
	if !ast.OutputType().IsExactType(cel.BoolType) {
		return nil, fmt.Errorf("binding: filter must be a boolean, not %s", ast.OutputType())
	}
	prg, err := env.Program(ast, cel.CostLimit(expr.DefaultCostLimit))
	if err != nil {
		return nil, fmt.Errorf("binding: filter: %w", err)
	}
	return &Filter{prg: prg}, nil
}

// Admits evaluates the filter over resolved parameter values. An evaluation
// error, such as a parameter that did not resolve, excludes the event.
func (f *Filter) Admits(values map[string]any) (bool, error) {
	if f == nil {
		return true, nil
	}
	v, _, err := f.prg.Eval(values)
	if err != nil {
		return false, fmt.Errorf("binding: filter: %w", err)
	}
	admitted, _ := v.Value().(bool)
	return admitted, nil
}

func celType(t mapping.FieldType) *cel.Type {
	switch t {
	case mapping.TypeString:
		return cel.StringType
	case mapping.TypeNumber:
		return cel.DoubleType
	case mapping.TypeBool:
		return cel.BoolType
	case mapping.TypeObject:
		return cel.MapType(cel.StringType, cel.DynType)
	case mapping.TypeArray:
		return cel.ListType(cel.DynType)
	default:
		return cel.DynType
	}
}
