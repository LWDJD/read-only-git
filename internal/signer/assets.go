package signer

import _ "embed"

// arweaveBundle 是 arweave-js 的浏览器构建，供签名页构造交易用。
//
// 内嵌而不是从 CDN 取：签名页跑在本机，不该依赖外网才能工作。
// 它的职责被限定在「构造交易 + 调钱包签名」，签完只把字段回传给 Go 侧，
// 由 Go 侧拼出完整交易并提交。私钥始终不出钱包。
//
//go:embed assets/arweave.bundle.js
var arweaveBundle []byte

// arweaveLicense 是上面那份构建的许可证原文（MIT）。
// 用了别人的代码就要把声明一并带上。
//
//go:embed assets/arweave-LICENSE.txt
var arweaveLicense []byte
