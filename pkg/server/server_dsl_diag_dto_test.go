package server

import (
	"encoding/json"
	"testing"

	"github.com/arketov/iterion/pkg/dsl/ir"
)

func TestIrDiagToDTOCarriesPosition(t *testing.T) {
	d := ir.Diagnostic{
		Code:      "C102",
		Severity:  ir.SeverityError,
		Message:   "m",
		NodeID:    "a",
		File:      "x.bot",
		Line:      6,
		Column:    3,
		EndLine:   6,
		EndColumn: 20,
		Property:  "compress",
	}
	dto := irDiagToDTO(d)
	if dto.File != "x.bot" || dto.Line != 6 || dto.Column != 3 ||
		dto.EndLine != 6 || dto.EndColumn != 20 || dto.Property != "compress" {
		t.Fatalf("position lost: %+v", dto)
	}

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"file", "line", "column", "end_line", "end_column", "property"} {
		if _, ok := m[k]; !ok {
			t.Errorf("JSON key %q missing: %s", k, raw)
		}
	}

	raw, err = json.Marshal(irDiagToDTO(ir.Diagnostic{Code: "C102", Severity: ir.SeverityError, Message: "m"}))
	if err != nil {
		t.Fatal(err)
	}
	m = nil
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"file", "line", "column", "end_line", "end_column", "property"} {
		if _, ok := m[k]; ok {
			t.Errorf("JSON key %q present without a position: %s", k, raw)
		}
	}
}
