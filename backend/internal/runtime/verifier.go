package runtime

import (
	"encoding/json"
	"strings"
)

type VerifyReport struct {
	Passed   bool     `json:"passed"`
	Missing  []string `json:"missing"`
	Evidence string   `json:"evidence,omitempty"`
}

func Verify(criteria []string, resultJSON string, toolEvidence int) VerifyReport {
	if len(criteria) == 0 {
		return VerifyReport{Passed: true, Evidence: resultJSON}
	}
	lower := strings.ToLower(resultJSON)
	var missing []string
	for _, c := range criteria {
		kw := keywords(c)
		hit := false
		for _, k := range kw {
			if k != "" && strings.Contains(lower, strings.ToLower(k)) {
				hit = true
				break
			}
		}
		if !hit && toolEvidence == 0 {
			missing = append(missing, c)
		}
	}
	b, _ := json.Marshal(map[string]any{"criteria": criteria, "tool_evidence": toolEvidence})
	_ = b
	return VerifyReport{Passed: len(missing) == 0, Missing: missing, Evidence: resultJSON}
}

func keywords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '，' || r == '、' || r == ',' || r == '。' || r == '"'
	})
}
