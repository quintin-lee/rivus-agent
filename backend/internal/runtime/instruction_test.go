package runtime

import (
	"strings"
	"sync"
	"testing"
)

func (r *EinoRunner) instructionSnapshot() string {
	r.mu.RLock()
	s := r.instruction
	r.mu.RUnlock()
	return s
}

func TestSkillInstructionAugment(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	if !strings.Contains(r.instructionSnapshot(), "你是 Rivus Agent") {
		t.Fatal("base instruction missing")
	}
	r.SetSkillInstructions("demo: does X")
	snap := r.instructionSnapshot()
	if !strings.Contains(snap, "可用技能") || !strings.Contains(snap, "demo: does X") {
		t.Fatalf("skills section not appended: %q", snap)
	}
	// idempotent: a second enable must NOT duplicate the section
	r.SetSkillInstructions("other: does Y")
	snap = r.instructionSnapshot()
	if strings.Count(snap, "可用技能") != 1 {
		t.Fatalf("skills section accumulated (Count=%d), want 1: %q", strings.Count(snap, "可用技能"), snap)
	}
	if !strings.Contains(snap, "other: does Y") || strings.Contains(snap, "demo: does X") {
		t.Fatalf("replace semantics broken: %q", snap)
	}
	// disable: empty listing removes the section, restores base
	r.SetSkillInstructions("")
	if r.instructionSnapshot() != "你是 Rivus Agent。用中文回答。必须基于工具返回的真实证据行动，禁止编造成功。需要多工具协作时分步调用并观察结果。" {
		t.Fatalf("disable must restore base instruction, got %q", r.instructionSnapshot())
	}
}

// TestSkillInstructionNoRace hammers the guarded writer + guarded reader under -race.
func TestSkillInstructionNoRace(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r.SetSkillInstructions(strings.Repeat("s", idx))
			_ = r.instructionSnapshot()
		}(i)
	}
	wg.Wait()
}
