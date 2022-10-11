func binaryTreePaths(root *TreeNode) []string {
	if root == nil {
		return nil
	}
	res := make([]string, 0)
	dfs(root, "", &res)
	return res
}

func dfs(root *TreeNode, str string, res *[]string) {
	if root == nil {
		return
	}
	str = fmt.Sprintf("%s%d->", str, root.Val)
	if root.Left == nil && root.Right == nil {
		*res = append(*res, str[:len(str)-2])
		return
	}
	dfs(root.Right, str, res)
	dfs(root.Left, str, res)
}
