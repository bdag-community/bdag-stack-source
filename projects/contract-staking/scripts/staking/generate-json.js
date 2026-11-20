const fs = require('fs');
const path = require('path');

async function main() {
    const artifactPath = path.join(__dirname, '../../artifacts/contracts/staking/StakingPool.sol/StakingContract.json');
    if (!fs.existsSync(artifactPath)) {
        console.error("Artifact not found. Please run 'npx hardhat compile' first.");
        process.exit(1);
    }

    const buildInfoPath = path.join(__dirname, '../../artifacts/build-info');
    if (!fs.existsSync(buildInfoPath)) {
        console.error("Build info directory not found.");
        process.exit(1);
    }

    const buildFiles = fs.readdirSync(buildInfoPath);

    // Find the build info that contains our contract
    let buildInfo;
    for (const file of buildFiles) {
        if (file.endsWith('.json')) {
            const info = JSON.parse(fs.readFileSync(path.join(buildInfoPath, file), 'utf8'));
            if (info.output.contracts['contracts/staking/StakingPool.sol'] && info.output.contracts['contracts/staking/StakingPool.sol']['StakingContract']) {
                buildInfo = info;
                break;
            }
        }
    }

    if (!buildInfo) {
        console.error("Build info not found.");
        process.exit(1);
    }

    // Create Standard JSON Input
    const standardJson = {
        language: buildInfo.input.language,
        sources: buildInfo.input.sources,
        settings: buildInfo.input.settings
    };

    fs.writeFileSync('standard-json-input.json', JSON.stringify(standardJson, null, 2));
    console.log("Successfully generated standard-json-input.json");
}

main().catch(console.error);
