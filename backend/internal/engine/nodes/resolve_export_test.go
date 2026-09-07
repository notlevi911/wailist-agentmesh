package nodes

// ResolveTemplateForTest exposes the unexported resolver to the external
// nodes_test package. Test-only; no production caller.
func ResolveTemplateForTest(s string, rc RunContexter) string {
	return resolveTemplate(s, rc)
}
