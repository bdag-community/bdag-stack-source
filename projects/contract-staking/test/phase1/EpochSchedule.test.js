const {expect} = require('chai');
const {ethers} = require('hardhat');
const fs = require('node:fs');
const { createHash } = require('node:crypto');
const vectors = require('./fixtures/epoch-schedule-vectors.json');
const max = (1n << 64n) - 1n;

describe('Versioned epoch arithmetic matches native Go vectors', function () {
  let clock;
  before(async () => { clock = await (await ethers.getContractFactory('EpochScheduleHarness')).deploy(); });
  it('pins the shared native/contract vectors without requiring a sibling checkout', function () {
    const bytes = fs.readFileSync(require.resolve('./fixtures/epoch-schedule-vectors.json'));
    expect(createHash('sha256').update(bytes).digest('hex')).to.equal('2540722554c6798a9fe71a8c8bf74afc932a6d2fc0273abd48d5c4ef732a4545');
  });
  it('preserves history and exact boundaries across two transitions and backwards timestamps', async () => {
    for (const c of vectors.cases) {
      const actual = await clock.at(vectors.segments, c.timestamp, c.floor);
      expect([...actual]).to.deep.equal(['epoch','version','start','end','duration'].map(k => BigInt(c[k])));
    }
  });
  it('does not reinterpret historical epochs using the new duration', async () => {
    for (let epoch = 0; epoch < 10; epoch++) {
      const before = await clock.bounds(vectors.segments.slice(0, 1), epoch);
      expect(before.start).to.equal(BigInt(1000 + epoch * 60));
      expect(await clock.bounds(vectors.segments, epoch)).to.deep.equal(before);
    }
  });
  it('rejects missing, zero-duration, discontinuous and repeated schedule segments', async () => {
    for (const segments of [[], [[0,1000,0]], [[1,1000,60]], [[0,1000,60],[10,1601,1800]], [[0,1000,60],[0,1000,1800]], [[0,1000,60],[10,1600,0]]]) {
      await expect(clock.validate(segments)).to.be.revertedWithCustomError(clock, 'InvalidEpochSchedule');
    }
  });
  it('rejects uint64 overflow consistently with the native engine', async () => {
    for (const segments of [[[0,max,60]], [[0,1000,60],[max,1000,1800]], [[0,max+1n,1]]]) {
      await expect(clock.validate(segments)).to.be.revertedWithCustomError(clock, 'EpochClockOverflow');
    }
    await expect(clock.at([[0,0,1]], max, 0)).to.be.revertedWithCustomError(clock, 'EpochClockOverflow');
    await expect(clock.at([[0,0,1]], 0, max)).to.be.revertedWithCustomError(clock, 'EpochClockOverflow');
    await expect(clock.at([[0,0,1]], max + 1n, 0)).to.be.revertedWithCustomError(clock, 'EpochClockOverflow');
    await expect(clock.at([[0,0,1]], 0, max + 1n)).to.be.revertedWithCustomError(clock, 'EpochClockOverflow');
    await expect(clock.bounds([[0,0,1]], max + 1n)).to.be.revertedWithCustomError(clock, 'EpochClockOverflow');
  });
  it('maps epoch boundaries back to the same epoch', async () => {
    for (const epoch of [0,1,9,10,11,12,13,100,1000000]) {
      const bounds = await clock.bounds(vectors.segments, epoch);
      expect(await clock.at(vectors.segments, bounds.start, 0)).to.deep.equal(bounds);
      expect(await clock.at(vectors.segments, bounds.end-1n, 0)).to.deep.equal(bounds);
    }
  });
});
