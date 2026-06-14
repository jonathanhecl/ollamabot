package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateSkillAndWriteValidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skills-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Test case 1: CreateSkill succeeds with valid parameters
	err = CreateSkill(tmpDir, "valid-skill", "A valid skill description", "http://homepage.com", "- Instruction 1\n- Instruction 2")
	if err != nil {
		t.Errorf("expected CreateSkill to succeed, got: %v", err)
	}

	// Test case 2: CreateSkill fails due to validation (empty instructions)
	err = CreateSkill(tmpDir, "invalid-skill", "A description", "http://homepage.com", "")
	if err == nil {
		t.Error("expected CreateSkill to fail with empty instructions, but it succeeded")
	}

	// Test case 3: WriteFile with "SKILL.md" validation
	wsDir := filepath.Join(tmpDir, "ws")
	_ = os.MkdirAll(wsDir, 0755)

	// 3a. Valid skill file write should succeed
	validSkillMD := `---
name: test-skill
description: some desc
homepage: http://test.com
---

## Description
some desc

## Instructions
- [ ] Do something
`
	err = WriteFile(wsDir, "my-skill/SKILL.md", validSkillMD)
	if err != nil {
		t.Errorf("expected WriteFile of valid SKILL.md to succeed, got: %v", err)
	}

	// 3b. Invalid skill file write (missing instructions/steps) should fail
	invalidSkillMD := `---
name: test-skill
description: some desc
homepage: http://test.com
---

## Description
some desc

## Instructions
`
	err = WriteFile(wsDir, "my-skill/SKILL.md", invalidSkillMD)
	if err == nil {
		t.Error("expected WriteFile of invalid SKILL.md to fail, but it succeeded")
	}
}
