package main

import "fmt"

func main() {
	basesubsidy := 100
	mul := 100
	div := 101
	count, total_reward := calculateRewardIteration(basesubsidy, mul, div)
	fmt.Println("count is ===> ", count)
	total_blocks := 951100 + 138240*count
	fmt.Println("total_blocks ===> ", total_blocks)
	time := total_blocks * 10 / 86400 / 365
	fmt.Println("time to reward zero is ===> ", time)
	fmt.Println("total reward is ===> ", total_reward)

}

func calculateRewardIteration(basesubsidy, mul, div int) (int, int) {
	count := 0
	total_reward := 100*951100 + 138240
	for basesubsidy > 0 {
		basesubsidy = basesubsidy * 100 / 101
		count++
		total_reward += 138240 * basesubsidy
	}
	return count, total_reward
}
