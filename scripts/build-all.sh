#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
BUILD_DIR="${PROJECT_ROOT}/build"
DIST_DIR="${PROJECT_ROOT}/dist"

# 版本优先级：第一个命令行参数 > VERSION 环境变量 > 当前 Git tag/提交。
VERSION="${1:-${VERSION:-}}"
if [[ -z "${VERSION}" ]]; then
    VERSION="$(git -C "${PROJECT_ROOT}" describe --tags --always --dirty 2>/dev/null || true)"
fi
if [[ -z "${VERSION}" ]]; then
    VERSION="dev"
fi

command -v go >/dev/null 2>&1 || {
    echo "错误：未找到 go，请先安装 Go 1.22 或更高版本。" >&2
    exit 1
}
command -v tar >/dev/null 2>&1 || {
    echo "错误：未找到 tar。" >&2
    exit 1
}
command -v zip >/dev/null 2>&1 || {
    echo "错误：未找到 zip。" >&2
    exit 1
}

cd "${PROJECT_ROOT}"

if [[ "${SKIP_TESTS:-0}" != "1" ]]; then
    echo "==> 运行测试"
    go test ./...
fi

# 每行格式：GOOS ASSET_OS GOARCH ARCHIVE。
TARGETS=(
    "linux linux amd64 tar.gz"
    "linux linux arm64 tar.gz"
    "darwin macos amd64 tar.gz"
    "darwin macos arm64 tar.gz"
    "windows windows amd64 zip"
    "windows windows arm64 zip"
)

# build 和 dist 都是本脚本专用的可再生产物目录，每次构建前清理旧内容。
rm -rf "${BUILD_DIR}" "${DIST_DIR}"
mkdir -p "${BUILD_DIR}" "${DIST_DIR}"

for target in "${TARGETS[@]}"; do
    read -r goos asset_os goarch archive <<<"${target}"

    binary_name="costrict-router"
    if [[ "${goos}" == "windows" ]]; then
        binary_name="costrict-router.exe"
    fi

    package_name="costrict-router_${VERSION}_${asset_os}_${goarch}"
    package_dir="${BUILD_DIR}/${package_name}"
    mkdir -p "${package_dir}"

    echo "==> 编译 ${asset_os}/${goarch}"
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
        go build -trimpath -o "${package_dir}/${binary_name}" ./cmd/costrict-router

    echo "==> 打包 ${package_name}.${archive}"
    if [[ "${archive}" == "zip" ]]; then
        (
            cd "${package_dir}"
            zip -9 -q "${DIST_DIR}/${package_name}.zip" "${binary_name}"
        )
    else
        (
            cd "${package_dir}"
            tar -czf "${DIST_DIR}/${package_name}.tar.gz" "${binary_name}"
        )
    fi
done

echo
echo "构建完成，发布产物位于：${DIST_DIR}"
find "${DIST_DIR}" -maxdepth 1 -type f -print | sort
