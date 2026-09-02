package skill

import (
	"errors"

	skillruntime "github.com/fwtllh-png/QCode/internal/adapter/skill"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
)

func recoverableSkillHandleError(err error) error {
	if !errors.Is(err, skillruntime.ErrSkillHandleInvalid) {
		return err
	}
	return tool.WithRecoveryHint(err, tool.RecoveryHint{
		ErrorCategory: skillruntime.ErrorCategoryHandleInvalid, RequiredAction: "skills_list",
	})
}
