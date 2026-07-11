package compat

import "testing"

func TestEstimateMessagesTokens(t *testing.T) {
	body := `{
		"model":"m",
		"system":"you are helpful",
		"messages":[
			{"role":"user","content":"hello world"},
			{"role":"user","content":[
				{"type":"text","text":"你好世界"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}
			]}
		],
		"tools":[{"name":"f","description":"desc","input_schema":{"type":"object"}}]
	}`
	n, err := EstimateMessagesTokens([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	// 期望量级：图片 1500 + 少量文本/工具；具体启发式可调，只锁定合理区间。
	if n < 1500 || n > 1600 {
		t.Fatalf("tokens = %d", n)
	}
	if _, err := EstimateMessagesTokens([]byte(`{"model":"m"}`)); err == nil {
		t.Fatal("缺少 messages 应报错")
	}
}
