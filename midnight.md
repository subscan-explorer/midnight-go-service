block detail
https://github.com/midnightntwrk/midnight-indexer/blob/a91843c39b87068f87777c62c65b8e5b7e32ab37/chain-indexer/src/infra/subxt_node/runtimes.rs#L155

## todo


## doc
https://github.com/kaleababayneh/create-midnight-app
https://docs.midnight.network/quickstart/user-quickstart
https://docs.midnight.network/develop/guides/wallet-dev-guide

```
transfer 1 usdt
from mn_shield-addr_test1ldysvguypvgd73ye8wrxj2mvntvad22d34sydgsed2eafgah7qrsxqx9syg5g84h4l7cnyagx9j57l7ww46vmc07z0fdghs3lhwqnw2ec5y880fp

to mn_shield-addr_test16wlc2a4jggr9jznaujdeylrgw5lqqucnzz0hcvahunnr4rm7hsnqxqzxckzp9fuyd0j2ryykg3kteaxjemlm0hy0ezjrsw5lxjazgzstx559z4w7
0xd3bf8576b24206590a7de49b927c68753e007313109f7c33b7e4e63a8f7ebc26030046c58412a7846be4a19096446cbcf4d2ceffb7dc8fc8a4383a9f34ba240a0b35
```


## rpc call
https://github.com/midnightntwrk/midnight-indexer/blob/b06fb56e7dd7066718865cba5b2e6420f712ae9b/chain-indexer/src/infra/subxt_node/runtimes.rs#L86


### example
deploy contract
https://midnight-testnet.subscan.io/event/1449392-2 合约地址 0x02 02008d561d6b06cfcd6a6655bd9fa14d208c6b9471ebf074378f7b6bfab1af8de821
node_modules/@midnight-ntwrk/midnight-js-contracts/dist/index.cjs

重复多次部署相同的合约，midnightRuntimeApi.getContractStateJson 返回的data数据(字节码？)是一致的

sha256 increment.prover
sha256 increment.verifier



# read contract 
contract/src/managed/counter/contract/index.cjs:142

call contract increment
https://midnight-testnet.subscan.io/extrinsic/1449917-2
faucet
https://midnight-testnet.subscan.io/extrinsic/1044846-2
https://midnight-testnet.subscan.io/extrinsic/1532330-2


# txn type
1. created_unshielded_utxos
2. spent_unshielded_utxos
https://github.com/midnightntwrk/midnight-indexer/blob/main/chain-indexer/src/domain/ledger_state.rs#L92


### index block
https://github.com/midnightntwrk/midnight-indexer/blob/e5e9af97d441bea3363d25907d4ac580ca00e524/chain-indexer/src/application.rs#L48
save ledger
https://github.com/midnightntwrk/midnight-indexer/blob/main/indexer-common/src/domain/ledger/ledger_state.rs#L89

block detail
https://github.com/midnightntwrk/midnight-indexer/blob/main/chain-indexer/src/infra/subxt_node/runtimes.rs

wallet detail
https://github.com/midnightntwrk/midnight-indexer/blob/fe9eb4c6cb5756cd4b3eee2e04a25a55c55859ca/wallet-indexer/src/infra/storage.rs

contract ledger detail
https://github.com/midnightntwrk/midnight-indexer/blob/fe9eb4c6cb5756cd4b3eee2e04a25a55c55859ca/indexer-common/src/domain/ledger/contract_state.rs#L52

transaction
https://github.com/midnightntwrk/midnight-indexer/blob/fe9eb4c6cb5756cd4b3eee2e04a25a55c55859ca/indexer-common/src/domain/ledger/ledger_state.rs#L89C12-L89C29

## wallet js
https://polkadot.js.org/apps/?rpc=wss%3A%2F%2Frpc.testnet-02.midnight.network#/explorer

## doc
https://github.com/kaleababayneh/create-midnight-app
https://docs.midnight.network/quickstart/user-quickstart


### serialize
https://github.com/midnightntwrk/midnight-ledger-prototype/tree/main/serialize


### ledger state

https://github.com/midnightntwrk/midnight-ledger-prototype/blob/ddd6d4a2e458f6ab5365fac3ce1b4994d60088a8/ledger/src/structure.rs#L2648

```rust
pub struct LedgerState<D: DB> {
    pub network_id: String,                   // 网络标识
    pub parameters: Sp<LedgerParameters, D>,  // 账本参数
    pub locked_pool: u128,                    // 锁定资金池
    pub bridge_receiving: Map<UserAddress, u128, D>, // 桥接接收资金
    pub reserve_pool: u128,                   // 储备池
    pub block_reward_pool: u128,              // 区块奖励池
    pub unclaimed_block_rewards: Map<UserAddress, u128, D, NightAnn>, // 未领取的区块奖励
    pub treasury: Map<TokenType, u128, D>,    // 国库
    pub zswap: Sp<zswap::ledger::State<D>, D>,// Zswap 状态（隐私相关资产交换）
    pub contract: Map<ContractAddress, ContractState<D>, D, NightAnn>, // 合约信息
    pub utxo: Sp<UtxoState<D>, D>,            // UTXO 状态
    pub replay_protection: Sp<ReplayProtectionState<D>, D>, // 重放保护状态
    pub dust: Sp<DustState<D>, D>,            // 零散资金管理
}
```
> 字段说明
1. network_id：标识当前账本归属的网络。
2. parameters：链上参数（如区块大小、手续费等），可动态调整。
3. locked_pool / reserve_pool / block_reward_pool / treasury：分别对应锁定池、储备池、区块奖励池、国库金额。
4. bridge_receiving：跨链桥资金接收。
5. unclaimed_block_rewards：各用户未领取的区块奖励。
6. zswap：链上隐私资产交换模块的状态。
7. contract：所有合约地址到其状态（余额、数据等）的映射。
8. utxo：UTXO 状态，管理未花费输出。
9. replay_protection：重放保护相关状态。
10. dust：零散资金的状态管理。

### witnesses

在 Midnight 的上下文中，**witnesses**（见证者）是指为智能合约提供私有输入的函数，通常由 DApp 或用户提供。它们的主要作用是让合约能够访问和使用本地的私有数据（如用户的密钥），而这些数据不会被公开在区块链上。

在 Compact 语言中，witnesses 的声明方式类似于 circuits（电路），但 witness 函数没有实现体，其具体实现由 TypeScript/JavaScript 层的 DApp 提供。例如：

```
witness getSecret(): Bytes<32>;
```

你可以在 circuits 里调用 witness 函数来获取私有数据，但 Compact 不允许你直接返回、存储或公开这些 witness 数据，除非你显式地用 `disclose()` 进行披露，否则编译器会报错。这种设计确保了隐私是默认的，只有在明确声明的情况下才会泄露私有信息 [Learn the basics of Compact, Midnight’s smart contract framework](https://docs.midnight.network/academy/module-5#learn-the-basics-of-compact-midnights-smart-contract-framework) [Declaring witnesses for private state](https://docs.midnight.network/develop/reference/compact/lang-ref#declaring-witnesses-for-private-state)。

在数学逻辑和零知识证明中，witness 也指“满足某个断言的值或函数”。在 Midnight 里，witness 是一种函数，其定义在 Compact 之外，通常用于访问和更新本地私有状态，这些操作不会被记录在公共账本上 [Glossary: Witness](https://docs.midnight.network/learn/glossary#witness)。

简而言之，witnesses 是 DApp 提供的私有输入接口，Compact 合约通过它们安全地获取用户本地的敏感数据，实现隐私保护。




### test case

部署bboard 合约 020010fe7dfa5adc6e4d0894af0a4e536cb6839cdcf797fbbc3cc9cdc434caf55929

部署 https://midnight-testnet.subscan.io/extrinsic/2299639-2

contract call
call post a message https://midnight-testnet.subscan.io/extrinsic/2299675-2
