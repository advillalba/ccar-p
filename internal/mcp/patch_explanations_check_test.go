package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ccar-p/study-platform/internal/exams"
)

type patchQRepo struct {
	existing exams.Question
	patched  exams.Question
	opts     *[]exams.Option
}

func (r *patchQRepo) Create(_ context.Context, q exams.Question) (exams.Question, error) {
	return q, nil
}
func (r *patchQRepo) Update(_ context.Context, q exams.Question) error { return nil }
func (r *patchQRepo) Patch(_ context.Context, q exams.Question, o *[]exams.Option) error {
	r.patched = q
	r.opts = o
	return nil
}
func (r *patchQRepo) Delete(_ context.Context, _ string) error              { return nil }
func (r *patchQRepo) Reorder(_ context.Context, _ string, _ []string) error { return nil }
func (r *patchQRepo) Load(_ context.Context, _ string) (exams.Question, error) {
	return r.existing, nil
}

func TestPatchQuestionOptionExplanationsMergesByKey(t *testing.T) {
	existing := exams.Question{
		ID: "q-1", ExamID: "e-1", DomainID: "d-1", Prompt: "P?", Explanation: "old",
		Difficulty: exams.Difficulty("beginner"), Position: 1,
		Options: []exams.Option{
			{ID: "o1", Key: "A", Text: "Alpha", Explanation: "old A", IsCorrect: true, Position: 1},
			{ID: "o2", Key: "B", Text: "Beta", Explanation: "old B", Position: 2},
		},
	}
	repo := &patchQRepo{existing: existing}
	registry := NewRegistry()
	if err := RegisterExamsTools(registry, &writeExams{}, repo, &writeOptions{}, &writePublishing{}); err != nil {
		t.Fatal(err)
	}
	tool, _ := registry.Get("patch_question")
	body := `{"id":"q-1","explanations":"new question explanation","option_explanations":[{"key":"B","explanation":"fixed B"}]}`
	if _, err := tool.Handler(&Context{Ctx: context.Background()}, json.RawMessage(body)); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if repo.patched.Explanation != "new question explanation" {
		t.Fatalf("explanations alias not applied: %q", repo.patched.Explanation)
	}
	if repo.opts == nil || len(*repo.opts) != 2 {
		t.Fatalf("expected merged 2 options, got %#v", repo.opts)
	}
	opts := *repo.opts
	if opts[0].Explanation != "old A" || !opts[0].IsCorrect || opts[0].Text != "Alpha" {
		t.Fatalf("option A changed unexpectedly: %#v", opts[0])
	}
	if opts[1].Explanation != "fixed B" || opts[1].Text != "Beta" || opts[1].IsCorrect {
		t.Fatalf("option B not merged: %#v", opts[1])
	}
}

func TestPatchQuestionExplanationsAlias(t *testing.T) {
	existing := exams.Question{
		ID: "q-1", ExamID: "e-1", DomainID: "d-1", Prompt: "P?", Explanation: "old",
		Difficulty: exams.Difficulty("beginner"), Position: 1,
		Options: []exams.Option{{ID: "o1", Key: "A", Text: "Alpha", Explanation: "x", IsCorrect: true, Position: 1}},
	}
	repo := &patchQRepo{existing: existing}
	registry := NewRegistry()
	if err := RegisterExamsTools(registry, &writeExams{}, repo, &writeOptions{}, &writePublishing{}); err != nil {
		t.Fatal(err)
	}
	tool, _ := registry.Get("patch_question")
	if _, err := tool.Handler(&Context{Ctx: context.Background()}, json.RawMessage(`{"id":"q-1","explanations":"only explanations field"}`)); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if repo.patched.Explanation != "only explanations field" {
		t.Fatalf("alias failed: %q", repo.patched.Explanation)
	}
	if repo.opts != nil {
		t.Fatalf("options should be nil when only explanation patched")
	}
}
