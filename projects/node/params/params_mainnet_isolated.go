package params

// MainNetIsolatedParams copies mainnet consensus settings for local isolated
// activation testing and must not be used for public mainnet nodes.
var MainNetIsolatedParams = func() Params {
	p := MainNetParams
	p.Name = "mainnet-isolated"
	p.DefaultPort = MainNetParams.DefaultPort
	p.Bootstrap = nil
	return p
}()
