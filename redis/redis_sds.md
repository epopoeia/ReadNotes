# redis动态字符串
sda(简单动态字符串，simple dynamic string)为二进制安全的(即使中间出现\0也没有影响)字符串，获取长度时间复杂度为O(1)，并且在操作前会判断sds的长度，不足则进行扩展，从而避免缓冲区溢出。

## 数据结构
sds数据结构即为普通字符串。
```c
typedef char *sds;
```
redis可以根据不同的字符串长度初始化不同的sds结构体,sdshdr5(未使用)，sdshdr8,sdshdr16,sdshdr32,sdshdr64。其中保存了长度以及分配的内存地址(不包括头部长度以及字符串结尾的\0)，flags低三位用来表示类型。
```c
struct __attribute__ ((__packed__)) sdshdr8 {
    uint8_t len; /* used */
    uint8_t alloc; /* excluding the header and null terminator */
    unsigned char flags; /* 3 lsb of type, 5 unused bits */
    char buf[];
};
```

    注：结构体参数`__attribute__ ((__packed__))`说明结构体取消内存对齐优化，按照实际占用字节数进行对齐。从而保持头部数据与实际数据连续，可以直接通过操作地址来寻找数据。

```c
#define SDS_TYPE_5  0
#define SDS_TYPE_8  1
#define SDS_TYPE_16 2
#define SDS_TYPE_32 3
#define SDS_TYPE_64 4
```
类型定义0-4所以flags三位即可表示。

根据字符长度自动类型扩展。
```c
static inline char sdsReqType(size_t string_size) {
    if (string_size < 1<<5)
        return SDS_TYPE_5;
    if (string_size < 1<<8)  // 256以下
        return SDS_TYPE_8;
    if (string_size < 1<<16) // 65536以下
        return SDS_TYPE_16;
    if (string_size < 1ll<<32) // 4294967296以下
        return SDS_TYPE_32;
    return SDS_TYPE_64;
}
```

## 原理分析
由于sdshdr5并没有被使用，因此接下来所有的描述仅针对8-64。

在sds中有两个重要的宏定义：
```c
#define SDS_HDR_VAR(T,s) struct sdshdr##T *sh = (void*)((s)-(sizeof(struct sdshdr##T)));
#define SDS_HDR(T,s) ((struct sdshdr##T *)((s)-(sizeof(struct sdshdr##T))))
```
其中 ##T表示链接，即SDS_HDR(8,s)，后面的sdshdr##T为sdshdr8。

- SDS_HDR_VAR宏将s位置的指针减去了结构体的大小，即指向了真实的sds的起始地址，并赋值给变量sh。
- SDS_HDR宏将地址s减去了结构体的大小，并转化成了指向sdshdr结构体的指针。

```c
// 根据initlen创建相应大小的sds
// 若init指定为SDS_NOINIT则不对内存进行初始化
// 若init为NULL否则初始化为零值，否则初始化为init的值
// 最后会在结尾填充'\0'，所以实际申请的长度为hdrlen+initlen+1
// hrdlen为对应sdshdrN结构体的大小
sds sdsnewlen(const void *init, size_t initlen)

// 调用sdsnwelen创建一个s的复制
sds sdsdup(const sds s) 

// 释放sds地址，如果s为空不进行任何操作，释放时要还原s指针
// 因为申请时返回的地址为跳过hdrlen的地址
void sdsfree(sds s)

// 更新s的长度，这是一个比较重要的函数，在我们修改了s之后，如果没有更新则长度不变
// 例如：s=sbsnew("123);s[1]='\0'
// 调用update之后长度变为1，update使用strlen获取长度，遇到'\0'停止
// 不调用update，通过sdslen获取长度依旧为3
// updat中先通过strlen获取长度之后使用sdssetlen设置s的长度
void sdsupdatelen(sds s)

// 清空字符串近将s[0]设为了'\0'并将长度设为0，并没有修改已分配的内存
void sdsclear(sds s)

// 将t指向的长度为len的字节写入s中，内部通过sdsMakeRoomFor扩展空间，然后直接memcpy，之后重新设置len以及结尾加入'\0'
sds sdscatlen(sds s, const void *t, size_t len)

// 获取s的子集
void sdsrange(sds s, ssize_t start, ssize_t end)

// 使用类似printf的格式在s后拼接字符串，基于sprintf() family functions，较慢
sds sdscatvprintf(sds s, const char *fmt, va_list ap)

// 更上一个函数类似，但是比它快，直接在S后面拼接字符串
sds sdscatfmt(sds s, char const *fmt, ...)

// 从两端移除cset中的字符
sds sdstrim(sds s, const char *cset)

// 比较两个sds，s1>s2为正数，小于负数，0相等
int sdscmp(const sds s1, const sds s2)

// 对s进行切割返回一个sds数组，count为元素个数
sds *sdssplitlen(const char *s, ssize_t len, const char *sep, int seplen, int *count)

// 用来释放前面函数生成的数组
void sdsfreesplitres(sds *tokens, int count)

// 为s拼接字符，非打印字符会被转换为"\x<hex-number>"
sds sdscatrepr(sds s, const char *p, size_t len)

// 将line转换为参数sds数组，argc为数组中元素个数，同样需要sdsfreesplitres来释放
sds *sdssplitargs(const char *line, int *argc)

// 将s中from的元素替换成对应的to元素
sds sdsmapchars(sds s, const char *from, const char *to, size_t setlen)

// 将argv中的每一个元素(除了最后一个)连接上sep，组成sds
sds sdsjoin(char **argv, int argc, char *sep)
```

## 用户API
```c
// 对s进行扩容，如果s的可用空间(alloc-len)满足addlen直接返回
// 否则计算newlen=addlen+len
// 如果newlen小于SDS_MAX_PREALLOC，则double，否则加上SDS_MAX_PREALLOC
// 扩容完成之后判断新的sds类型，相同的话直接执行realloc，否则重新malloc并复制内容，最后设置len以及alloc
sds sdsMakeRoomFor(sds s, size_t addlen)

// 对s重新分配空间以释放多余空间，但会引起指针改变
// 如果sdslen返回值可以用更小的type保存则直接使用更小的type来保存s
// 如果新老type一致，或者type大于8，则直接调用realloc由分配器决定是否移动内存以整理碎片
sds sdsRemoveFreeSpace(sds s)

// 已分配大小包含四部分
// 1.sds头部大小，位置为s-sdshdrlen，位于指针前面
// 2.string的实际大小
// 3.尾部空闲部分
// 4.终止字符'\0'
size_t sdsAllocSize(sds s)

// 返回sds分配内存的首地址，也就是结构体的首地址；通常首地址指向的是字符串的起点。通过减去头部大小来获取。
void *sdsAllocPtr(sds s)

// 此函数结合sdsMakeRoomFor
// sdsMakeRoomFor 函数扩容之后，新的sds中len为原值，alloc扩大
// 此时向sds中写入数据，len并没有发生改变
// 调用此函数传入写入的数值，修改len
// 亦可用于减少长度
void sdsIncrLen(sds s, ssize_t incr)
```

## 总结
合理利用空间，支持动态扩容。