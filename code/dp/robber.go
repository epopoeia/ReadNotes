package main

func rob(nums []int) int {
	n := len(nums)
	if n == 0 {
		return 0
	}
	if len(nums) == 1 {
		return nums[0]
	}
	dp := make([]int, n)
	dp[0] = nums[0]
	dp[1] = maxf(nums[0], nums[1])
	for i := 2; i < n; i++ {
		dp[i] = maxf(dp[i-2]+nums[i], dp[i-1])
	}
	return dp[n-1]
}

func maxf(a, b int) int {
	if a > b {
		return a
	}
	return b
}
