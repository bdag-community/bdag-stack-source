const { expect } = require("chai");
const { ethers, network } = require("hardhat");
const { time } = require("@nomicfoundation/hardhat-network-helpers");

const minimalEpoch = (epoch) => epoch === 0n ? "0x" : ethers.toBeHex(epoch);
const tallySlot = (miner, epoch) => ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode(
  ["address", "bytes32"], [miner, ethers.keccak256(minimalEpoch(BigInt(epoch)))]));
const rewardDomain = ethers.keccak256(ethers.toUtf8Bytes("bdag.reward.voting.v1"));
const rewardEpochBase = (epoch) => ethers.keccak256(ethers.concat([rewardDomain, ethers.zeroPadValue(ethers.toBeHex(epoch), 32)]));
const rewardTallySlot = (miner, epoch) => ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode(
  ["address", "bytes32"], [miner, rewardEpochBase(epoch)]));
const uintMappingSlot = (key, base) => ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode(["uint256", "uint256"], [key, base]));
const storage = (address, slot, value) => network.provider.send("hardhat_setStorageAt", [address, ethers.toQuantity(BigInt(slot)), ethers.toBeHex(value, 32)]);

describe("ChainTallyReader preserves legacy audit reads and exposes native reward epochs", function () {
  let source, reader, miners;
  beforeEach(async function () {
    miners = (await ethers.getSigners()).slice(1, 5);
    source = await ethers.deployContract("CredentialVotingSource");
    reader = await ethers.deployContract("ChainTallyReader", [await source.getAddress()]);
  });
  it("reads minimally encoded Go epochs including zero and multi-byte values", async function () {
    for (const epoch of [0n, 1n, 255n, 256n, 65536n]) {
      await storage(await reader.getAddress(), tallySlot(miners[0].address, epoch), 123n + epoch);
      expect(await reader.legacyTallyOf(miners[0].address, epoch)).to.equal(123n + epoch);
    }
  });
  it("reads finalized native reward totals without registration or keeper iteration", async function () {
    await storage(await reader.getAddress(), 4, 3n); // native reward ledger is in epoch 3
    await storage(await reader.getAddress(), 5, 1n); // bootstrap source epoch 0 + 1
    await storage(await reader.getAddress(), 6, 2n); // first complete source epoch 1 + 1
    await storage(await reader.getAddress(), uintMappingSlot(1n, 7n), 1000n);
    await storage(await reader.getAddress(), rewardTallySlot(miners[0].address, 1n), 730n);
    expect(reader.interface.hasFunction("registerMiner")).to.equal(false);
    expect(reader.interface.hasFunction("finalizeEpoch")).to.equal(false);
    expect(await reader.isFinalized(0)).to.equal(false);
    expect(await reader.isFinalized(1)).to.equal(true);
    expect(await reader.isFinalized(3)).to.equal(false);
    expect(await reader.totalAt(1)).to.equal(1000n);
    expect(await reader.weightAt(miners[0].address, 1)).to.equal(730n);
    await expect(reader.totalAt(0)).to.be.revertedWithCustomError(reader, "EpochNotFinalized");
  });
});
