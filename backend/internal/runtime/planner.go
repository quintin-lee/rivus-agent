package runtime

import "strings"

type PlanStep struct {
	Index       int
	Description string
	DependsOn   []int
	Verify      string
}

func SelectMode(requested string, goal string) string {
	if requested == "plan_execute" {
		return "plan_execute"
	}
	if requested == "react" {
		return "react"
	}
	g := strings.ToLower(goal)
	markers := []string{"步骤", "计划", "多步", "分阶段", "step", "plan", "阶段"}
	for _, mk := range markers {
		if strings.Contains(g, mk) {
			return "plan_execute"
		}
	}
	return "react"
}

func BuildInitialPlan(goal string, criteria []string) []PlanStep {
	steps := []PlanStep{{Index: 0, Description: "理解目标并收集必要信息：" + goal}}
	for i, c := range criteria {
		steps = append(steps, PlanStep{Index: i + 1, Description: "达成验收：" + c, DependsOn: []int{i}, Verify: c})
	}
	steps = append(steps, PlanStep{Index: len(steps), Description: "汇总结果并对照成功标准自检", DependsOn: []int{len(steps) - 1}})
	return steps
}
