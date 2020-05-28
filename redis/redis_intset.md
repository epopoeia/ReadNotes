# redis-intset
相关文件
- intset.h
- intset.c
看名字，整数集合么
特点是数据总是有序的

## 数据结构

```c
typedef struct intset {
    uint32_t encoding; //编码方式
    uint32_t length; // 长度
    int8_t contents[]; //内容
} intset;

// 编码方式
/* Note that these encodings are ordered, so:
 * INTSET_ENC_INT16 < INTSET_ENC_INT32 < INTSET_ENC_INT64. */
#define INTSET_ENC_INT16 (sizeof(int16_t))
#define INTSET_ENC_INT32 (sizeof(int32_t))
#define INTSET_ENC_INT64 (sizeof(int64_t))
```

## API接口

```c
intset *intsetNew(void); zmalloc一个intset，缺省编码是INTSET_ENC_INT16，contents为空
intset *intsetAdd(intset *is, int64_t value, uint8_t *success); 添加元素会排序的
intset *intsetRemove(intset *is, int64_t value, int *success); 移除一个元素，会引起该元素之后的所有元素的整体移动；不会降级
uint8_t intsetFind(intset *is, int64_t value); 二分查找一个元素
int64_t intsetRandom(intset *is); 随机返回一个元素，基于random()
uint8_t intsetGet(intset *is, uint32_t pos, int64_t *value); 返回指定元素
uint32_t intsetLen(const intset *is); 返回is->length
size_t intsetBlobLen(intset *is); 返回is总共占内存多少字节
```

```c
// 插入元素，会升级
intset *intsetAdd(intset *is, int64_t value, uint8_t *success) {
    // 其实就是判断数据所在的范围来决定是否升级
    uint8_t valenc = _intsetValueEncoding(value);
    uint32_t pos;
    if (success) *success = 1;

    // 升级
    if (valenc > intrev32ifbe(is->encoding)) {
        // 不需要success肯定越界了
        return intsetUpgradeAndAdd(is,value);
    } else {
       //  先查一下，找到了就返回
        if (intsetSearch(is,value,&pos)) {
            if (success) *success = 0;
            return is;
        }

        // 要插入先扩容
        is = intsetResize(is,intrev32ifbe(is->length)+1);
        // 在尾部直接追加就好了
        if (pos < intrev32ifbe(is->length)) intsetMoveTail(is,pos,pos+1);
    }

    // set到对应的位置，其余的要后移
    _intsetSet(is,pos,value);
    is->length = intrev32ifbe(intrev32ifbe(is->length)+1);
    return is;
}
```

ifbe 判断是不是大端