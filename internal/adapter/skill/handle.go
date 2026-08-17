package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func candidateMatchesHandle(item candidate, handle string) bool {
	switch handle[:4] {
	case "skh_":
		return skillHandle(item) == handle
	case "skp_":
		return skillPackageHandle(item) == handle
	case "skr_":
		return skillResourceHandle(item) == handle
	default:
		return false
	}
}

func skillHandle(item candidate) string {
	return boundedSkillHandle("skh", strings.Join([]string{
		item.metadata.Name, string(item.source), item.plugin, item.digest,
		fmt.Sprint(item.authority.Generation), item.authority.Token,
	}, "\x00"))
}

func skillPackageHandle(item candidate) string {
	return boundedSkillHandle("skp", strings.Join([]string{
		string(item.source), item.plugin, item.digest,
		fmt.Sprint(item.authority.Generation), item.authority.Token,
	}, "\x00"))
}

func skillResourceHandle(item candidate) string {
	return boundedSkillHandle("skr", strings.Join([]string{
		skillPackageHandle(item), item.relative, item.digest,
	}, "\x00"))
}

func boundedSkillHandle(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + "_" + hex.EncodeToString(sum[:20])
}

func validSkillHandle(value string) bool {
	if len(value) != 44 ||
		(!strings.HasPrefix(value, "skh_") &&
			!strings.HasPrefix(value, "skp_") &&
			!strings.HasPrefix(value, "skr_")) {
		return false
	}
	_, err := hex.DecodeString(value[4:])
	return err == nil
}
