package main

// execABI is the ABI fragment for the ProposalExecuted event.
const execABI = `[
  {
    "anonymous": false,
    "inputs": [
      { "indexed": true,  "name": "id",   "type": "uint256" },
      { "indexed": false, "name": "link", "type": "string"  },
      { "indexed": false, "name": "sha256Hash", "type": "string" }
    ],
    "name": "ProposalExecuted",
    "type": "event"
  }
]`