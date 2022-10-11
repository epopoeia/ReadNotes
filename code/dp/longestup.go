package main

func lengthOfLIS(nums []int) int {
	n := len(nums)
	if n == 0 {
		return 0
	}
	dp := make([]int, n)
	max := 0
	for i := 0; i < n; i++ {
		for j := 0; j < i; j++ {
			if nums[i] > nums[j] {
				dp[i] = maxf1(dp[j]+1, dp[i])
			}
		}
		max = maxf1(dp[i], max)
	}
	return max + 1
}

func maxf1(a, b int) int {
	if a > b {
		return a
	}
	return b
}
