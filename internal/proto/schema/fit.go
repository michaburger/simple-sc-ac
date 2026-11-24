package schema

// Fit returns the values that the schema defines for a message part, and drops the rest.
// Callers build one superset of fields for all versions, and Fit keeps what the version
// of the session knows. Missing required fields are then found by the validation before
// sending.
func (s *Schema) Fit(op *Operation, part Part, values map[string]any) map[string]any {
	fields := s.MessageFields(op, part)
	out := make(map[string]any, len(fields))
	for n := range fields {
		if v, ok := values[n]; ok && v != nil {
			out[n] = v
		}
	}
	return out
}

// Has reports whether the message part defines a field.
func (s *Schema) Has(op *Operation, part Part, field string) bool {
	_, ok := s.MessageFields(op, part)[field]
	return ok
}
