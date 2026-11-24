package schema

// Example returns a message part with a valid value for every field, optional and
// conditional fields included, without command and opId. It is used by tests and by the
// simulators to produce messages straight from a schema file.
func (s *Schema) Example(op *Operation, part Part) map[string]any {
	return s.exampleObject(s.MessageFields(op, part), 0)
}

func (s *Schema) exampleObject(fields Fields, depth int) map[string]any {
	out := make(map[string]any, len(fields))
	for n, f := range fields {
		out[n] = s.exampleValue(f.Type, f, depth)
	}
	return out
}

func (s *Schema) exampleValue(typ string, f *Field, depth int) any {
	switch typ {
	case "any", "string":
		if f != nil && f.Format == "semver" {
			return s.Version
		}
		return "example"
	case "bool":
		return true
	case "object":
		return map[string]any{}
	case "enum":
		return f.Values[0]
	case "number":
		if f != nil && f.Min != nil {
			return *f.Min
		}
		return 1.5
	case "integer":
		return uint64(1)
	case "eui64":
		return uint64(0x0200000000000001)
	case "bytes":
		n := 2
		if f != nil && f.Len != nil {
			n = *f.Len
		}
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i + 1)
		}
		return b
	case "array":
		n := 2
		if f.Len != nil {
			n = *f.Len
		}
		arr := make([]any, n)
		for i := range arr {
			arr[i] = s.exampleValue(f.Items, nil, depth+1)
		}
		return arr
	}
	if td, ok := s.Types[typ]; ok && depth < 4 {
		return s.exampleObject(td.Fields, depth+1)
	}
	return map[string]any{}
}
