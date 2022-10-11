package main

func longestPalindrome(s string) string {
	n := len(s)
	ans := ""
	dp := make([][]int, n)
	for i := 0; i < n; i++ {
		dp[i] = make([]int, n)
	}
	for l := 0; l < n; l++ {
		for r := 0; l+r < n; r++ {
			j := r + l
			if l == 0 {
				dp[r][j] = 1
			} else if l == 1 {
				if s[r] == s[j] {
					dp[r][j] = 1
				}
			} else {
				if s[r] == s[j] {
					dp[r][j] = dp[r+1][j-1]
				}
			}
			if dp[r][j] > 0 && l+1 > len(ans) {
				ans = s[r : r+l+1]
			}
		}
	}
	return ans
}
