require('hardhat-storage-layout');
require("@nomicfoundation/hardhat-toolbox");
require("@openzeppelin/hardhat-upgrades");
require("dotenv").config();
const { installOpenZeppelinLondonBytesHook } = require("./compat/openzeppelin-5.4-london/hardhat-hook.cjs");
installOpenZeppelinLondonBytesHook(__dirname);

const londonSettings = {
    optimizer: { enabled: true, runs: 200 },
    evmVersion: "london",
    outputSelection: { "*": { "*": ["storageLayout"] } }
};

/** @type import('hardhat/config').HardhatUserConfig */
module.exports = {
    solidity: {
        compilers: [{ version: "0.8.24", settings: londonSettings }],
        // The versioned Governor requires IR to fit EIP-170. Keep every other
        // contract on the historical London pipeline and optimizer settings.
        overrides: {
            "contracts/governance/phase1/VersionedEpochGovernor.sol": {
                version: "0.8.24",
                settings: { ...londonSettings, viaIR: true }
            }
        }
    },
    networks: {
        hardhat: {
            chainId: 8008,
            hardfork: "london",
            allowUnlimitedContractSize: false,
            accounts: { count: 64 }
        },
        awakening: {
            url: process.env.AWAKENING_RPC_URL || "https://rpc.awakening.network",
            chainId: parseInt(process.env.AWAKENING_CHAIN_ID || "54211"),
            accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : []
        },
        localhost: {
            url: process.env.LOCAL_RPC_URL || "http://127.0.0.1:18545",
            chainId: parseInt(process.env.LOCAL_CHAIN_ID || "1048"),
            accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : []
        },
        // DigiMount devnet (3-node Docker net on the Zerotier box). Signer order matters:
        // [0] = contract owner, [1] = faucet/premine, [2] = legacy PRIVATE_KEY fallback.
        devnet: {
            url: process.env.DEVNET_RPC_URL || "http://127.0.0.1:28545",
            chainId: parseInt(process.env.DEVNET_CHAIN_ID || "8008"),
            accounts: [process.env.DEVNET_OWNER_KEY, process.env.DEVNET_FAUCET_KEY, process.env.PRIVATE_KEY].filter(Boolean),
            timeout: 120000
        },
        mainnet: {
            url: process.env.MAINNET_RPC_URL || "http://52.8.80.249:18545",
            chainId: parseInt(process.env.MAINNET_CHAIN_ID || "1404"),
            accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : []
        }
    },
    paths: {
        sources: "./contracts",
        tests: "./test",
        cache: "./cache",
        artifacts: "./artifacts"
    },
    mocha: {
        // Storage-layout and bytecode compatibility checks inspect full compiler
        // build-info. Cold, pinned-toolchain containers need more than the
        // default budget even though the checks are deterministic and bounded.
        timeout: 120000
    }
};
