package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	resp := Build([]SourceModel{
		{ID: "glm-5", ContextWindow: 198000, SupportsImages: false},
		{ID: "kimi-k2.5", ContextWindow: 256000, SupportsImages: true},
	})
	if len(resp.Models) != 2 {
		t.Fatalf("应生成 2 个模型, got=%d", len(resp.Models))
	}
	first := resp.Models[0]
	if first.Slug != "glm-5" || first.DisplayName != "glm-5" {
		t.Fatalf("slug/display_name 应为模型 ID, got slug=%q display=%q", first.Slug, first.DisplayName)
	}
	// visibility=list 才会出现在 codex /model 选择器里。
	if first.Visibility != "list" {
		t.Fatalf("visibility 应为 list, got=%q", first.Visibility)
	}
	// base_instructions 必须非空，否则选中该模型时 codex 系统提示会被清空。
	if first.BaseInstructions == "" || first.BaseInstructions != codexBaseInstructions {
		t.Fatal("base_instructions 应为内嵌的 codex 提示词且非空")
	}
	// priority 用序号保持选择器顺序。
	if resp.Models[0].Priority != 0 || resp.Models[1].Priority != 1 {
		t.Fatalf("priority 应按序号递增, got=%d,%d", resp.Models[0].Priority, resp.Models[1].Priority)
	}
	// 上下文窗口取自上游真实值。
	if first.ContextWindow != 198000 || first.MaxContextWindow != 198000 {
		t.Fatalf("context_window 应为上游真实值 198000, got=%d/%d", first.ContextWindow, first.MaxContextWindow)
	}
	// 不支持图片：input_modalities 仅 text。
	if len(first.InputModalities) != 1 || first.InputModalities[0] != "text" {
		t.Fatalf("不支持图片应只有 text, got=%v", first.InputModalities)
	}
	// 支持图片：input_modalities 含 image。
	if got := resp.Models[1].InputModalities; len(got) != 2 || got[1] != "image" {
		t.Fatalf("支持图片应为 [text image], got=%v", got)
	}
}

func TestBuildDefaultsContextWindow(t *testing.T) {
	// 上游未提供上下文窗口（0）时回退到默认值。
	resp := Build([]SourceModel{{ID: "x", ContextWindow: 0}})
	if resp.Models[0].ContextWindow != defaultContextWindow {
		t.Fatalf("缺省上下文窗口应回退到 %d, got=%d", defaultContextWindow, resp.Models[0].ContextWindow)
	}
}

func TestMarshalIndented(t *testing.T) {
	data, err := Build([]SourceModel{{ID: "Auto", ContextWindow: 100000}}).MarshalIndented()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	// 顶层必须是 {"models":[...]}（codex ModelsResponse 格式）。
	var top struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("结果不是合法 JSON: %v\n%s", err, data)
	}
	if len(top.Models) != 1 {
		t.Fatalf("应含 1 个模型, got=%d", len(top.Models))
	}
	// 必填 Vec 字段必须序列化为 []（而非 null），否则 codex 反序列化失败。
	s := string(data)
	if !strings.Contains(s, `"supported_reasoning_levels": []`) {
		t.Fatalf("supported_reasoning_levels 应为 [], got: %s", s)
	}
	if !strings.Contains(s, `"experimental_supported_tools": []`) {
		t.Fatalf("experimental_supported_tools 应为 [], got: %s", s)
	}
	// 带末尾换行，便于直接写文件。
	if data[len(data)-1] != '\n' {
		t.Fatal("输出应以换行结尾")
	}
}

func TestBuildEmpty(t *testing.T) {
	// 空列表也必须是 {"models":[]}（非 null），codex 才能正常解析。
	data, err := Build(nil).MarshalIndented()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(data), `"models": []`) {
		t.Fatalf("空列表应序列化为 \"models\": [], got: %s", data)
	}
}
