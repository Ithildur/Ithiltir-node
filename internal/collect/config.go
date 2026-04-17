package collect

type Config struct {
	PreferredNICs []string
	Debug         bool
}

const thinpoolCachePath = "/run/ithiltir-node/thinpool.json"
