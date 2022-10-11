package minroute

func minPathSum(grid [][]int) int {
	if len(grid) == 0 {
		return 0
	}
	dp := make([][]int, len(grid))
	for i := range grid {
		dp[i] = make([]int, len(grid[i]))
	}
	for i := range grid {
		for j := range grid[i] {
			if i == 0 && j == 0 {
				dp[i][j] = grid[0][0]
			} else if i == 0 {
				dp[i][j] = dp[i][j-1] + grid[i][j]
			} else if j == 0 {
				dp[i][j] = dp[i-1][j] + grid[i][j]
			} else {
				dp[i][j] = min(dp[i-1][j], dp[i][j-1]) + grid[i][j]
			}
		}
	}
	return dp[len(grid)-1][len(grid[0])-1]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
