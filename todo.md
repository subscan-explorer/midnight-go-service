- [x] ``列出与合约相关的所有公开交易``。
- [x] ``合约验证``
- [x] ``显示合约地址、部署**字节码**(verification key) 和可验证源代码（如果公开)``。
- [x] 呈现公共账本状态，包括紧凑类型，例如 STATE、Maybe、Counter、Bytes 和 Opaque。(abi call,**UI**)
- [x] 显示交易执行导致的公开状态变化。 例如：counter +1
- [x] ``呈现合约的电路定义(circuit definitions)和见证函数(witness function)，包括其名称和 输入/输出类型(contract-info.json)``
- [x] 显示合约维护权限 (CMA) 信息（如果已公开）。``contractState.maintenanceAuthority.committee`` https://docs.midnight.network/develop/tutorial/creating/updatability#how-to-operate-a-maintenance-authority https://docs.midnight.network/develop/reference/midnight-api/ledger/classes/ContractMaintenanceAuthority
- [x] 为已验证合约提供基于 ABI 的用户界面，以协助公共电路交互。
- [x] 在不泄露机密数据的情况下，指示交易中是否存在零知识证明。(midnight 相关的所有交易都是 zk)，ui
- [x] 链接到 Compact 语言文档，以实现智能合约透明度。 https://docs.midnight.network/develop/reference/compact/ ui
- [x] 可视化 Merkle 树根哈希值并允许包含证明检查（如果数据公开可用）。(？需要compact 合约包括 readable merkle tree) https://docs.midnight.network/develop/how-midnight-works/keeping-data-private#the-commitmentnullifier-pattern
  https://docs.midnight.network/academy/module-3


```rust
pub struct ContractState<D: DB> {
    pub data: ChargedState<D>,
    pub operations: HashMap<EntryPointBuf, ContractOperation, D>,
    pub maintenance_authority: ContractMaintenanceAuthority,
    pub balance: HashMap<TokenType, u128, D>,
}

pub struct ChargedState<D: DB> {
    pub(crate) state: Sp<StateValue<D>, D>,
    // TODO: it would be better to generate charged keys from `data`, since it's
    // an invariant that the chargable contract state is always a subset of the
    // `charged_keys`. I assume this implies a manual `Arbitrary`
    // implementation, but maybe this is some `proptest` magic that supports
    // deriving this ...
    #[cfg_attr(feature = "proptest", proptest(value = "RcMap::default()"))]
    pub(crate) charged_keys: RcMap<D>,
}

pub struct ContractMaintenanceAuthority {
    pub committee: Vec<VerifyingKey>,
    pub threshold: u32,
    pub counter: u32,
}
```
