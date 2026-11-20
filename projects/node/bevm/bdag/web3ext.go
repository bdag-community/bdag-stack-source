package bdag

import "github.com/ethereum/go-ethereum"

const BdagJs = `
web3._extend({
	property: '',
	methods: [
		new web3._extend.Method({
			name: 'getPeerInfo',
			call: '_getPeerInfo',
			params: 2,
			inputFormatter: [null, null]
		}),

		new web3._extend.Method({
			name: 'removeBan',
			call: '_removeBan',
			params: 1,
			inputFormatter: [null]
		}),
		new web3._extend.Method({
			name: 'setRpcMaxClients',
			call: '_setRpcMaxClients',
			params: 1,
		}),
		new web3._extend.Method({
			name: 'setLogLevel',
			call: '_setLogLevel',
			params: 1,
		}),

		new web3._extend.Method({
			name: 'checkAddress',
			call: '_checkAddress',
			params: 2,
		}),
		new web3._extend.Method({
			name: 'getBalance',
			call: '_getBalance',
			params: 2,
		}),
		new web3._extend.Method({
			name: 'getAddresses',
			call: '_getAddresses',
			params: 1,
		}),

		new web3._extend.Method({
			name: 'getBlockhash',
			call: '_getBlockhash',
			params: 1,
		}),
		new web3._extend.Method({
			name: 'getBlockhashByRange',
			call: '_getBlockhashByRange',
			params: 2,
		}),
		new web3._extend.Method({
			name: 'getBlockByOrder',
			call: '_getBlockByOrder',
			params: 4,
			inputFormatter: [null, null, null, null]
		}),
		new web3._extend.Method({
			name: 'getBlock',
			call: '_getBlock',
			params: 4,
			inputFormatter: [null, null, null, null]
		}),
		new web3._extend.Method({
			name: 'getBlockV2',
			call: '_getBlockV2',
			params: 4,
			inputFormatter: [null, null, null, null]
		}),
		new web3._extend.Method({
			name: 'getBlockHeader',
			call: '_getBlockHeader',
			params: 2,
		}),
		new web3._extend.Method({
			name: 'isOnMainChain',
			call: '_isOnMainChain',
			params: 1,
		}),
		new web3._extend.Method({
			name: 'getBlockWeight',
			call: '_getBlockWeight',
			params: 1,
		}),
		new web3._extend.Method({
			name: 'getBlockByID',
			call: '_getBlockByID',
			params: 4,
			inputFormatter: [null, null, null, null]
		}),
		new web3._extend.Method({
			name: 'getBlockByNum',
			call: '_getBlockByNum',
			params: 4,
			inputFormatter: [null, null, null, null]
		}),
		new web3._extend.Method({
			name: 'isBlue',
			call: '_isBlue',
			params: 1,
		}),
		new web3._extend.Method({
			name: 'getCoinbase',
			call: '_getCoinbase',
			params: 2,
			inputFormatter: [null, null]
		}),
		new web3._extend.Method({
			name: 'getFees',
			call: '_getFees',
			params: 1,
		}),

		new web3._extend.Method({
			name: 'getMempool',
			call: '_getMempool',
			params: 2,
			inputFormatter: [null, null]
		}),
		new web3._extend.Method({
			name: 'estimateFee',
			call: '_estimateFee',
			params: 1,
		}),

		new web3._extend.Method({
			name: 'getBlockTemplate',
			call: '_getBlockTemplate',
			params: 2,
		}),
		new web3._extend.Method({
			name: 'submitBlock',
			call: '_submitBlock',
			params: 1,
		}),
		new web3._extend.Method({
			name: 'getRemoteGBT',
			call: '_getRemoteGBT',
			params: 1,
		}),
		new web3._extend.Method({
			name: 'submitBlockHeader',
			call: '_submitBlockHeader',
			params: 1,
		}),
		new web3._extend.Method({
			name: 'generate',
			call: '_generate',
			params: 1,
		}),

		new web3._extend.Method({
			name: 'createRawTransaction',
			call: '_createRawTransaction',
			params: 3,
		}),
		new web3._extend.Method({
			name: 'createRawTransactionV2',
			call: '_createRawTransactionV2',
			params: 3,
		}),
		new web3._extend.Method({
			name: 'decodeRawTransaction',
			call: '_decodeRawTransaction',
			params: 1,
		}),
		new web3._extend.Method({
			name: 'sendRawTransaction',
			call: '_sendRawTransaction',
			params: 2,
		}),
		new web3._extend.Method({
			name: 'getRawTransaction',
			call: '_getRawTransaction',
			params: 2,
		}),
		new web3._extend.Method({
			name: 'getUtxo',
			call: '_getUtxo',
			params: 3,
		}),
		new web3._extend.Method({
			name: 'getRawTransactions',
			call: '_getRawTransactions',
			params: 7,
		}),
		new web3._extend.Method({
			name: 'getRawTransactionByHash',
			call: '_getRawTransactionByHash',
			params: 2,
		}),
		new web3._extend.Method({
			name: 'txSign',
			call: '_txSign',
			params: 3,
		}),
		new web3._extend.Method({
			name: 'createTokenRawTransaction',
			call: '_createTokenRawTransaction',
			params: 9,
		}),
		new web3._extend.Method({
			name: 'createImportRawTransaction',
			call: '_createImportRawTransaction',
			params: 2,
		}),
		new web3._extend.Method({
			name: 'createExportRawTransaction',
			call: '_createExportRawTransaction',
			params: 4,
		}),
		new web3._extend.Method({
			name: 'createExportRawTransactionV2',
			call: '_createExportRawTransactionV2',
			params: 3,
		}),
	],
	properties: [
		new web3._extend.Property({
			name: 'getNodeInfo',
			getter: '_getNodeInfo'
		}),
		new web3._extend.Property({
			name: 'getRpcInfo',
			getter: '_getRpcInfo'
		}),
		new web3._extend.Property({
			name: 'getTimeInfo',
			getter: '_getTimeInfo'
		}),
		new web3._extend.Property({
			name: 'getNetworkInfo',
			getter: '_getNetworkInfo'
		}),
		new web3._extend.Property({
			name: 'getSubsidy',
			getter: '_getSubsidy'
		}),
		new web3._extend.Property({
			name: 'stop',
			getter: '_stop'
		}),
		new web3._extend.Property({
			name: 'banlist',
			getter: '_banlist'
		}),

		new web3._extend.Property({
			name: 'getBestBlockHash',
			getter: '_getBestBlockHash'
		}),
		new web3._extend.Property({
			name: 'getBlockCount',
			getter: '_getBlockCount'
		}),
		new web3._extend.Property({
			name: 'getBlockTotal',
			getter: '_getBlockTotal'
		}),
		new web3._extend.Property({
			name: 'getMainChainHeight',
			getter: '_getMainChainHeight'
		}),
		new web3._extend.Property({
			name: 'getOrphansTotal',
			getter: '_getOrphansTotal'
		}),
		new web3._extend.Property({
			name: 'isCurrent',
			getter: '_isCurrent'
		}),
		new web3._extend.Property({
			name: 'tips',
			getter: '_tips'
		}),
		new web3._extend.Property({
			name: 'getTokenInfo',
			getter: '_getTokenInfo'
		}),

		new web3._extend.Property({
			name: 'getMempoolCount',
			getter: '_getMempoolCount'
		}),
		new web3._extend.Property({
			name: 'saveMempool',
			getter: '_saveMempool'
		}),

		new web3._extend.Property({
			name: 'getMinerInfo',
			getter: '_getMinerInfo'
		}),

		new web3._extend.Property({
			name: 'getVMsInfo',
			getter: '_getVMsInfo'
		}),
	]
});
`

func init() {
	ethereum.Modules[""] = BdagJs
}
