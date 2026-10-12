package containers

import "math"

// minMemory is the smallest memory limit Docker accepts.
const minMemory = 6 << 20

// maxMemory (1 PiB) is far above any host and keeps 2 x memory (the swap, see memorySwap) inside int64.
const maxMemory = 1 << 50

// maxCPUs is far above any host (Linux builds at most 8192 CPUs) and keeps cpus x 1e9 exact and inside int64.
const maxCPUs = 1_000_000

// Resources are the limits of the form: Memory in bytes, CPUs in cores (1.5 = one and a half); 0 is no limit.
type Resources struct {
	Memory int64   `json:"memory"`
	CPUs   float64 `json:"cpus"`
}

// nanoCPUs is cpus as Docker's NanoCPUs, rounded, so that 0.1+0.2 and 0.3 are the same limit. Only for a validated
// value (Spec.Validate): converting a float beyond the range of int64 gives an unspecified result.
func nanoCPUs(cpus float64) int64 { return int64(math.Round(cpus * 1e9)) }

// cpusOf is Docker's NanoCPUs as cores.
func cpusOf(nano int64) float64 { return float64(nano) / 1e9 }

// memorySwap is the MemorySwap (memory and swap together) for the memory limit memory of a container whose limits were
// baseMemory and baseSwap: none without a memory limit (Docker refuses a swap limit alone), unlimited stays unlimited,
// Docker's default (no swap limit, or twice the memory) follows the new limit, and a swap limit set outside edelweiss
// stays as long as it is not below the memory limit.
func memorySwap(baseMemory, baseSwap, memory int64) (int64, error) {
	switch {
	case memory == 0:
		return 0, nil
	case baseSwap == -1:
		return -1, nil
	case baseSwap == 0 || baseSwap == 2*baseMemory:
		return 2 * memory, nil
	case baseSwap >= memory:
		return baseSwap, nil
	default:
		return 0, fieldErr("resources.memory", "must not be more than %d MiB, the limit of memory and swap together that was set outside edelweiss", baseSwap>>20)
	}
}
