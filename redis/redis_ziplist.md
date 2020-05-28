# redis-ziplist
ziplist在早期设计中元素程度比较小或者数量比较少的时候会使用，它存储在一段连续的内存上，所以效率比较高，但是频繁的修改会导致频繁的内存申请与释放，当长度特别长时，一次realloc会导致大量的内存拷贝。

实际上是作为一种编码方式存在的。
## ziplist格式

```c
  <zlbytes> <zltail> <zllen> <entry> <entry> ... <entry> <zlend>
```

    注：所有字段无特殊说明均为小端

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| zlbytes | uint32_t | 用于保存压缩链表长度(单位为字节)，包括4字节的本身。可以快速调整压缩链表大小无需遍历。 |
| ztail | uint32_t | 用于记录链表中最后一个节点的偏移。可以迅速从队列尾部取出节点。 |
| zllen | uint16_t | 记录压缩链表中节点个数。当节点数大于等于UINT16_MAX(65535)时，需要遍历整个链表才能得到数量(一旦超过上限即使删除尾部节点使数量进入正常范围这个字段依旧保持最大值不变)。 |
| zlend | uint8_t | 用于表示压缩链表末尾的特殊节点，值为255。普通节点不会以255开头，因此可以判断节点第一个字节是否为255来检查末尾。 |

## 压缩链表节点结构

压缩链表每个节点以两个字段开头

- 第一个字段是上一个节点的长度(prevlen)，用于从后向前遍历
- 第二个字段是该节点的编码方式(encoding)，指示本节点是字节数组还是整数。如果是字节数组还需要包含数组长度。

通常情况下节点格式如下
```c
<prevlen> <encoding> <entry-data>
```
对于一些很小的数字，encoding本身就可以包含数字本身。

```c
<prevlen> <encoding>
```

prevlen编码方式为：

    如果长度小于254字节，则prevlen只需要一个字节(utf8_t)表示。当长度大于254字节时，则需要5个字节，第一个字节为254表示后面跟着一个32位整数。
    | prevlen 0-253 | encoding | entry_data |
    | oxFE<4字节无符号整数，小端> | encoding | entry-data |

节点数据不同，encoding字段编码如下

| 编码 | 大小/字节 | 说明 |
| - | - | - |
| 00pppppp | 1 | 保存小于等于63个字符的字节数组(6位)，pppppp表示6比特大小的无符号整数长度 |
| 01pppppp\|qqqqqqqq | 2 | 保存小于等于16383个字符的字节数组(14位)。NOTE：14比特的数字以大端保存。 |
| 10000000\|qqqqqqqq\|rrrrrrrr\|ssssssss\|tttttttt | 5 | 保存大于等于16384，小于等于32^2-1个字符的字节数组(32位)，第一个字节的低6位未使用。NOTE：32比特的数字是以大端保存的 |
| 11000000 | 3 | 保存2字节有符号整数(int16_t)的编码 |
| 11010000 | 5 | 保存4字节有符号整数(int32_t)的编码 |
| 11100000 | 9 | 保存8字节有符号整数(int64_t)的编码 |
| 11110000 | 4 | 保存3字节有符号整数(24位)的编码 |
| 11111110 | 2 | 保存1字节有符号整数(int8_t)的编码 |
| 1111xxxx | 3 | (xxxx取值为0001-1101)保存0-12的整数编码，xxxx为0001是表示保存的为0，以此类推 |
| 11111111 | 1 | 压缩链表的末尾节点 |

当节点保存的是整数时，以小端格式保存。

## 例子

如下链表包含了数字2和5由15个字节组成
```python
[0f 00 00 00] [0c 00 00 00] [02 00] [00 f3] [02 f6] [ff]
      |             |          |       |       |     |
   zlbytes        zltail    entries   "2"     "5"   end
```

前4字节为15表示链表总字节数，接下来4字节表示最后一个节点的偏移，这里是12指向5。接下来2字节表示压缩链表中节点个数这里是2。00 f3 表示列表中第一个节点，代表数组2，第一个字节表示上一个节点的长度，头节点所以为0，第二个字节为编码方式，采用了上述1111xxxx编码，下一个节点同理。尾部节点为ff特殊字符。

向上述节点中添加“hello world”，新增节点内容为

```python
[02] [0b] [48 65 6c 6c 6f 20 57 6f 72 6c 64]
```
此节点将插入到5后面，02表示上一个节点长度，0b表示编码方式，对应00pppppp，pppppp部分对应b为11，刚好等于hello world长度，接下来48字节为ASCII。

## 原理分析
链表其他的操作比较简单，直接调用API即可，这里来记一下更新的原理，这个比较复杂。
### 链表更新

由于每一个节点都保存了前一个节点的长度，并且根据前一节点prevlen字节数由1-5不等，因此当插入一个节点时可能会产生连锁反应，后续的节点都需要修改。
具体的实现在下面的函数中。

```c
unsigned char *__ziplistCascadeUpdate(unsigned char *zl, unsigned char *p)
```
其中p指向的是第一个需要更新的节点，通过后重新计算大小之后重新分配内存，然后修改相应的尾节点偏移等，直到遇到结束或者下一个节点不需要修改为止。但是prevlen字节不会被压缩，比如5字节，在修改节点之后只需要1个字节，并不会压缩为1个字节，为了方式后续扩容引起的大规模内存移动。

### API列表

```c
// 创建新的ziplist
unsigned char *ziplistNew(void);

// 将second追加到first后面，内部会扩容其中一个，并把另一个设为null释放空间
unsigned char *ziplistMerge(unsigned char **first, unsigned char **second);

// 压入数据，where表示头部还是尾部
#define ZIPLIST_HEAD 0
#define ZIPLIST_TAIL 1
unsigned char *ziplistPush(unsigned char *zl, unsigned char *s, unsigned int slen, int where);

// 返回zl中p的下一个节点，p为尾部返回null
unsigned char *ziplistNext(unsigned char *zl, unsigned char *p);

// 返回zl中p的前一个节点，p为头部返回null
unsigned char *ziplistPrev(unsigned char *zl, unsigned char *p);

// 获取p指向的内容，整数放到sval中，数组放入sstr中，p指向ff返回0否则为1
unsigned int ziplistGet(unsigned char *p, unsigned char **sstr, unsigned int *slen, long long *sval);

// 将数据插入的p的为止，后面后移
unsigned char *ziplistInsert(unsigned char *zl, unsigned char *p, unsigned char *s, unsigned int slen);

// 删除p指向节点并原地更新p因此可以在迭代过程中删除节点
unsigned char *ziplistDelete(unsigned char *zl, unsigned char **p);

// 删除范围内节点
unsigned char *ziplistDeleteRange(unsigned char *zl, int index, unsigned int num);

// 相等为1 否则为0
unsigned int ziplistCompare(unsigned char *p, unsigned char *s, unsigned int slen);

// p指向节点skip个节点后的内容与vstr相等的节点没找到返回null
unsigned char *ziplistFind(unsigned char *p, unsigned char *vstr, unsigned int vlen, unsigned int skip);
```

## 总结
- 压缩链表用来减少内存占用，其内存连续
- 通过多种编码方式有效压缩数据
- 修改过程需要移动内存，成本高
- 节点数达到上限之后必须要遍历，删除节点之后一样要遍历获取长度