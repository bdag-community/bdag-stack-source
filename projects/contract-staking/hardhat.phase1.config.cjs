// Local-only governance build. Deliberately does not import live network config or dotenv.
require("@nomicfoundation/hardhat-toolbox");
require("@openzeppelin/hardhat-upgrades");
const { installOpenZeppelinLondonBytesHook } = require("./compat/openzeppelin-5.4-london/hardhat-hook.cjs");
installOpenZeppelinLondonBytesHook(__dirname);

const label = process.env.BDAG_BUILD_LABEL || "integration";
if (!/^[a-z0-9-]+$/.test(label)) throw new Error("Invalid local build label");
const chainId = Number(process.env.BDAG_FIXTURE_CHAIN_ID || "8008");
if (![8008, 1043].includes(chainId)) throw new Error("Invalid local fixture chain ID");

const standardCompiler = {
    version: "0.8.24",
    settings: {
      optimizer: { enabled: true, runs: 200 },
      evmVersion: "london",
      outputSelection: { "*": { "*": ["storageLayout"] } },
    },
};
module.exports = {
  solidity: {
    compilers: [standardCompiler],
    // Only the new versioned Governor uses IR optimization. Historical release
    // artifacts remain pinned separately; do not regenerate their manifest.
    overrides: {
      "contracts/governance/phase1/VersionedEpochGovernor.sol": {
        ...standardCompiler,
        settings: { ...standardCompiler.settings, viaIR: true },
      },
    },
  },
  networks: {
    hardhat: {
      chainId,
      hardfork: "london",
      allowUnlimitedContractSize: false,
      accounts: { count: 64 },
      ...(process.env.BDAG_FIXTURE_DATE ? { initialDate: process.env.BDAG_FIXTURE_DATE } : {}),
    },
  },
  paths: {
    sources: "./contracts",
    tests: "./test/phase1",
    cache: `./.phase1-cache/${label}`,
    artifacts: `./.phase1-artifacts/${label}`,
  },
  mocha: { timeout: 60000 },
};
