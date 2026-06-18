// Package version 保存当前程序版本；正式构建时通过 -ldflags 注入 Release tag。
package version

// Current 是当前程序版本。直接执行 go build 时保持 dev，避免开发构建误报更新。
var Current = "dev"
