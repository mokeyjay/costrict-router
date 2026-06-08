package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	resp := Build([]string{"Auto", "Tencent-kimi-k2.6"})
	if len(resp.Models) != 2 {
		t.Fatalf("应生成 2 个模型, got=%d", len(resp.Models))
	}
	first := resp.Models[0]
	if first.Slug != "Auto" || first.DisplayName != "Auto" {
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
}

func TestMarshalIndented(t *testing.T) {
	data, err := Build([]string{"Auto"}).MarshalIndented()
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
