package main

import "fmt"

type TreeNode struct {
	Val   int
	Left  *TreeNode
	Right *TreeNode
}

// var res []int

// func preorderTraversal(root *TreeNode) []int {
// 	res = []int{}
// 	dfs(root)
// 	return res
// }

// func dfs(root *TreeNode) {
// 	if root != nil {
// 		res = append(res, root.Val)
// 		dfs(root.Left)
// 		dfs(root.Right)
// 	}
// }

// func preorderTraversal(root *TreeNode) []int {
// 	var res []int
// 	var stack []*TreeNode

// 	for 0 < len(stack) || root != nil { //root != nil 只为了第一次root判断，必须放最后
// 		for root != nil {
// 			res = append(res, root.Val)       //前序输出
// 			stack = append(stack, root.Right) //右节点 入栈
// 			root = root.Left                  //移至最左
// 		}
// 		index := len(stack) - 1 //栈顶
// 		root = stack[index]     //出栈
// 		stack = stack[:index]
// 	}
// 	return res
// }

// // 前序转链表
// func preorderTraversal(root *TreeNode) []int {
// 	var max *TreeNode
// 	var res []int
// 	for root != nil {
// 		if root.Left == nil {
// 			res = append(res, root.Val)
// 			root = root.Right
// 		} else {
// 			max = root.Left
// 			for max.Right != nil {
// 				max = max.Right
// 			}

// 			root.Right, max.Right = root.Left, root.Right
// 			root.Left = nil
// 		}
// 	}
// 	return res
// }

// 莫里斯中序
func inorderTraversal(root *TreeNode) []int {
	var max *TreeNode
	var res []int
	for root != nil {
		if root.Left == nil {
			res = append(res, root.Val)
			root = root.Right
		} else {
			max = root.Left
			for max.Right != nil && max.Right != root {
				max = max.Right
			}

			if max.Right == nil {
				max.Right = root
				root = root.Left
			} else {
				max.Right = nil
				res = append(res, root.Val)
				root = root.Right
			}
		}
	}
	return res
}

// 保持树结构
func preorderTraversal(root *TreeNode) []int {
	var max *TreeNode
	var res []int
	for root != nil {
		if root.Left == nil {
			res = append(res, root.Val)
			root = root.Right
		} else {
			max = root.Left
			for max.Right != nil && max.Right != root {
				max = max.Right
			}

			if max.Right == nil {
				res = append(res, root.Val)
				max.Right = root.Right
				root = root.Left
			} else {
				root = root.Right
				max.Right = nil
			}
		}
	}
	return res
}

// 原地转链表
func flatten(root *TreeNode) {
	if root == nil {
		return
	}
	flatten(root.Left)
	if root.Left != nil {
		l := root.Left
		for l.Right != nil {
			l = l.Right
		}
		l.Right = root.Right
		root.Right = root.Left
		root.Left = nil
	}
	flatten(root.Right)
}

func flatten1(root *TreeNode) {
	curr := root
	for curr != nil {
		if curr.Left != nil {
			next := curr.Left
			predecessor := next
			for predecessor.Right != nil {
				predecessor = predecessor.Right
			}
			predecessor.Right = curr.Right
			curr.Left, curr.Right = nil, next
		}
		curr = curr.Right
	}
}

func main() {
	root := &TreeNode{Val: 1}
	left := &TreeNode{Val: 2}
	right := &TreeNode{Val: 3}
	left1 := &TreeNode{Val: 4}
	left2 := &TreeNode{Val: 5}
	left.Left = left1
	left.Right = left2
	root.Left = left
	root.Right = right
	fmt.Println(preorderTraversal(root))

}
