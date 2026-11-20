const { expect } = require("chai");
const { ethers } = require("hardhat");

describe("OpenZeppelin Bytes London compatibility", function () {
  it("preserves clamped slices across empty, unaligned and word-boundary inputs", async function () {
    const h = await ethers.deployContract("LondonBytesHarness");
    for (const length of [0, 1, 20, 31, 32, 33, 64, 65, 97]) {
      const input = Uint8Array.from({ length }, (_, i) => (i * 73 + 19) % 256);
      for (const start of [0, 1, 20, 31, 32, length, length + 17]) {
        for (const end of [0, 1, 31, 32, length, length + 19]) {
          const clampedEnd = Math.min(end, length);
          const clampedStart = Math.min(start, clampedEnd);
          expect(await h.slice(input, start, end)).to.equal(ethers.hexlify(input.slice(clampedStart, clampedEnd)));
        }
      }
    }
    expect(await h.slice("0x123456", ethers.MaxUint256, ethers.MaxUint256)).to.equal("0x");
  });
});
