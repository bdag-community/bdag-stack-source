// Shared helpers for the staking test suites. Slot math mirrors the Go engine
// (blockdag-corechain/bevm/bdag/consensus/staking.go calculateMappingSlot).
const hre = require("hardhat");
const { ethers, network } = hre;

const coder = ethers.AbiCoder.defaultAbiCoder();
const slot1 = (key, base) => ethers.keccak256(coder.encode(["address", "uint256"], [key, base]));
// mapping(address k1 => mapping(address k2 => uint)) : keccak(pad(k2) ++ keccak(pad(k1) ++ base))
const slot2 = (k1, k2, base) => ethers.keccak256(coder.encode(["address", "bytes32"], [k2, slot1(k1, base)]));
const ERC1967_IMPL_SLOT = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc";

async function rawSlot(addr, slot) { return BigInt(await ethers.provider.getStorage(addr, slot)); }

// Simulate the consensus engine crediting the 27% staker share: poolTotalAssets (slot 5) += amount,
// backed by native balance on the contract (the engine also adds balance).
async function engineCreditPool(proxyAddr, pool, amount) {
    const s = slot1(pool, 5);
    const cur = await rawSlot(proxyAddr, s);
    await network.provider.send("hardhat_setStorageAt", [proxyAddr, ethers.toQuantity(BigInt(s)), ethers.toBeHex(cur + amount, 32)]);
    const bal = await ethers.provider.getBalance(proxyAddr);
    await network.provider.send("hardhat_setBalance", [proxyAddr, ethers.toQuantity(bal + amount)]);
}

const WEEK = 7 * 24 * 3600;
const PERIODS = { INITIAL_BONDING: 4 * WEEK, UNBONDING: 3 * WEEK, EPOCH: WEEK };

module.exports = { slot1, slot2, rawSlot, engineCreditPool, ERC1967_IMPL_SLOT, PERIODS, WEEK };
