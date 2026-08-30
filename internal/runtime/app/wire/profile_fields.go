package wire

func mutableSessionProfileFields(
	modelFields []string,
	toolCalls bool,
	approvalMutable bool,
) []string {
	fields := append(
		[]string{"mode", "max_steps"},
		modelFields...,
	)
	if toolCalls {
		fields = append(fields, "enabled_tool_ids")
	}
	if approvalMutable {
		fields = append(fields, "approval_posture")
	}
	return fields
}
