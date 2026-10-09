package skill

import (
	"fmt"
	"regexp"
	"strings"
)

var dangerous = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-rf\s+/`),
	regexp.MustCompile(`(?i)\bcurl\b.*\|\s*sh`),
	regexp.MustCompile(`(?i)\bchmod\s+777`),
	regexp.MustCompile(`(?i)AKIA[0-9A-Z]{16}`),
}

func Validate(name, body string) error {
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "/") {
		return fmt.Errorf("invalid skill name")
	}
	if len(body) > 64*1024 {
		return fmt.Errorf("skill body too large")
	}
	for _, re := range dangerous {
		if re.MatchString(body) {
			return fmt.Errorf("skill contains dangerous content matched by %q", re.String())
		}
	}
	return nil
}
