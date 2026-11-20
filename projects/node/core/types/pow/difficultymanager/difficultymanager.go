package difficultymanager

import (
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
)

func NewDiffManager(con model.Consensus, cfg *params.Params) model.DifficultyManager {
	switch cfg.PowConfig.DifficultyMode {
	case pow.DIFFICULTY_MODE_KASPAD:
		return &kaspadDiff{
			con:                            con,
			b:                              con.BlockChain(),
			powMax:                         cfg.PowConfig.BdagXKeccakV1PowLimit,
			difficultyAdjustmentWindowSize: int(cfg.WorkDiffWindowSize),
			disableDifficultyAdjustment:    false,
			targetTimePerBlock:             cfg.TargetTimePerBlock,
			genesisBits:                    cfg.PowConfig.BdagXKeccakV1PowLimitBits,
			cfg:                            cfg,
		}
	case pow.DIFFICULTY_MODE_DEVELOP:
		return &developDiff{
			b:   con.BlockChain(),
			cfg: cfg,
		}
	}
	return &bdagDiff{
		con: con,
		b:   con.BlockChain(),
		cfg: cfg,
	}
}
