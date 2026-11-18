2.2 剩余的 功能点

- [ ] 解码并显示合约调用中使用的公开可见输入参数 ``目前只有部分支持，可以显示交易的entity data``。
- [ ] (未来支持）一旦 Midnight 支持事件发送，即可启用事件日志可视化。
- [ ] 在公开适用的情况下，可视化与 Zswap 相关的元素，例如无效化符和承诺。(?) zswap readable 不包含 nullifier 和 commitment,需要增加相关field
- [ ] 衡量并公开以下相关指标： ``需要项目方提供``
  • 输入/输出效率
  • 证明验证时间
  • 交易应用和验证时间
  • 执行对虚拟机性能的影响
- [ ] 测试网水龙头性能, 
```graphql
query {
  transactions(offset: { hash: "3031323..." }) {
    id
    hash
    unshieldedCreatedOutputs {
      owner
      value
      tokenType
      intentHash
      outputIndex
    }
    unshieldedSpentOutputs {
      owner
      value
      tokenType
      intentHash
      outputIndex
    }
  }
}
```
