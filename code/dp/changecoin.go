package main

func coinChange(coins []int, amount int) int {
	n := len(coins)
	dp := make([][]int, n+1)
	for i := 0; i <= n; i++ {
		dp[i] = make([]int, amount+1)
	}
	for j := 0; j <= amount; j++ {
		dp[0][j] = amount + 1
	}
	dp[0][0] = 0
	for i := 1; i <= n; i++ {
		for j := 0; j <= amount; j++ {
			if j < coins[i-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = min(dp[i][j-coins[i-1]]+1, dp[i-1][j])
			}
		}
	}
	if dp[n][amount] > amount {
		return -1
	}
	return dp[n][amount]
}

func coinChange1(coins []int, amount int) int {
	n := len(coins)
	dp := make([]int, amount+1)
	for i := 0; i <= amount; i++ {
		dp[i] = amount + 1
	}
	dp[0] = 0
	for i := 1; i <= n; i++ {
		for j := 0; j <= amount; j++ {
			if j >= coins[i-1] {
				dp[j] = min(dp[j-coins[i-1]]+1, dp[j])
			}
		}
	}
	if dp[amount] > amount {
		return -1
	}
	return dp[amount]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
