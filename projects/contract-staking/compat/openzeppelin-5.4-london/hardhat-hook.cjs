const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const { subtask } = require("hardhat/config");
const { TASK_COMPILE_SOLIDITY_READ_FILE } = require("hardhat/builtin-tasks/task-names");
const provenance = require("./PROVENANCE.json");

const hash = (value) => crypto.createHash("sha256").update(value).digest("hex");

function installOpenZeppelinLondonBytesHook(projectRoot) {
  const originalPath = path.resolve(projectRoot, provenance.originalPath);
  const replacementPath = path.join(__dirname, "Bytes.sol");
  subtask(TASK_COMPILE_SOLIDITY_READ_FILE).setAction(async ({ absolutePath }, hre, runSuper) => {
    const content = await runSuper();
    if (absolutePath !== originalPath) return content;
    if (hash(content) !== provenance.originalSha256) throw new Error("OpenZeppelin source changed; review London compatibility patch");
    const replacement = fs.readFileSync(replacementPath, "utf8");
    if (hash(replacement) !== provenance.replacementSha256) throw new Error("London compatibility source hash mismatch");
    return replacement;
  });
}

module.exports = { installOpenZeppelinLondonBytesHook };
