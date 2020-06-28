# redis-radix tree

相关文件
- rax.h
- rax.c

基数树是一种空间优化的trie(前缀树)数据结构，如果树中的节点有唯一子节点，那么父子将合并，通常用来构建key为字符串的关联数组，路由表结构等。

[]节点表示key在树中，()表示不在。

```c
/*
 *                  ["foo"] ""
 *                     |
 *                  [t   b] "foo"
 *                  /     \
 *        "foot" ("er")    ("ar") "foob"
 *                 /          \
 *       "footer" []          [] "foobar"
 */
```
上述树插入"first"会导致分裂

```c
/*
 *                    (f) ""
 *                    /
 *                 (i o) "f"
 *                 /   \
 *    "firs"  ("rst")  (o) "fo"
 *              /        \
 *    "first" []       [t   b] "foo"
 *                     /     \
 *           "foot" ("er")    ("ar") "foob"
 *                    /          \
 *          "footer" []          [] "foobar"
*/
```
删除时可能会导致压缩。

## 数据结构

```c
#define RAX_NODE_MAX_SIZE ((1<<29)-1)
typedef struct raxNode {
    uint32_t iskey:1;     /* 当前节点是否存储key */
    uint32_t isnull:1;    /* key关联的value为null不保存 */
    uint32_t iscompr:1;   /* 节点是否压缩 */
    uint32_t size:29;     /* 子节点数量或者压缩字符串的长度 */
    /* data中存储当前节点的数据，按顺序存储
     *
     * [header iscompr=0][abc][a-ptr][b-ptr][c-ptr](value-ptr?)
     *
     * [header iscompr=1][xyz][z-ptr](value-ptr?)
     */
    unsigned char data[];
} raxNode;

typedef struct rax {
    raxNode *head;
    uint64_t numele;
    uint64_t numnodes;
} rax;
```

## 查找过程

查找时指定一个字符串以及它的长度，对树中的边进行匹配。

```c
// 查找匹配字符串s，长度为len
// stopnode匹配停止的节点指针
// plink父节点指向匹配节点的指针
// splitpos为当前压缩节点匹配的位置，可以用来在插入时切割字符串，0表示，父节点完成匹配不需要当前节点字符
// 匹配上时返回值与len相等
static inline size_t raxLowWalk(rax *rax, unsigned char *s, size_t len, raxNode **stopnode, raxNode ***plink, int *splitpos, raxStack *ts) {
```

## 插入操作

插入时同样类似于查找，先找到stopnode，拿到splitpos，splitpos为0直接插入到plink中，不然将节点从splitpos位置分裂为父节点的两个子节点。

## 删除过程
删除节点时，直接清掉压缩节点的部分数据，删除节点时需要考虑合并。

## 总结

- key的长度为k，radix tree查找时间负责度为O (K)
- 重新平衡过程时间复杂度通常为 O(log N)
- 劣势为key必须是字符串