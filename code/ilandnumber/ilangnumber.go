package main

func numIslands(grid [][]byte) int {
	count := 0
	for line := range grid {
		for column := range grid[line] {
			if grid[line][column] == '0' {
				continue
			}

			count++
			dfs(grid, line, column)
		}
	}
	return count
}

func dfs(grid [][]byte, i, j int) {
	if i < 0 || i >= len(grid) || j < 0 || j >= len(grid[i]) {
		return
	}

	if grid[i][j] != '1' {
		return
	}

	grid[i][j] = '0'
	dfs(grid, i+1, j)
	dfs(grid, i-1, j)
	dfs(grid, i, j+1)
	dfs(grid, i, j-1)
}

func numIslands1(grid [][]byte) int {
	count := 0
	queue := make([]int, 0)
	nr := len(grid)
	if nr == 0 {
		return 0
	}
	nc := len(grid[0])
	for i := 0; i < nr; i++ {
		for j := 0; j < nc; j++ {
			if grid[i][j] == '1' {
				count++
				grid[i][j] = '0'
				queue = append(queue, i*nc+j)
			}
			for len(queue) != 0 {
				a := queue[0]
				queue = queue[1:]
				r := a / nc
				c := a % nc
				if r-1 >= 0 && grid[r-1][c] == '1' {
					queue = append(queue, (r-1)*nc+c)
					grid[r-1][c] = '0'
				}
				if r+1 < nr && grid[r+1][c] == '1' {
					queue = append(queue, (r+1)*nc+c)
					grid[r+1][c] = '0'
				}
				if c-1 >= 0 && grid[r][c-1] == '1' {
					queue = append(queue, r*nc+c-1)
					grid[r][c-1] = '0'
				}
				if c+1 < nc && grid[r][c+1] == '1' {
					queue = append(queue, r*nc+c+1)
					grid[r][c+1] = '0'
				}
			}
		}
	}
	return count
}

func numIslands2(grid [][]byte) int {
	count := 0
	for line := range grid {
		for column := range grid[line] {
			if grid[line][column] == '0' {
				continue
			}

			count++
			dfs(grid, line, column)
		}
	}
	return count
}
