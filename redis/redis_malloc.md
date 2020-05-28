# redis内存分配

redis中支持多种内存分配器默认使用jemalloc内存分配器，也可在编译时指定使用其他的内存分配器。通过宏定义将不同分配器的函数映射为malloc以及free等(应该是门面模式？)。同时注释中还写到redis团队修改过的jemalloc是可以支持碎片整理的。

## zmalloc函数

```c
void *zmalloc(size_t size) {
    // 分配地址空间大小为size+PREFIX_SIZE，PREFIX_SIZE用来存储快大小即头部
    void *ptr = malloc(size+PREFIX_SIZE);

    // 分配失败直接调用oom，默认处理为打日志
    if (!ptr) zmalloc_oom_handler(size);
// 判断是否可以使用zmalloc_size获取内存块的实际大小
#ifdef HAVE_MALLOC_SIZE
    update_zmalloc_stat_alloc(zmalloc_size(ptr));
    return ptr;
#else
    // 保存分配数据的实际大小
    *((size_t*)ptr) = size;
    // 更新使用内存大小(内存对齐后)
    update_zmalloc_stat_alloc(size+PREFIX_SIZE);
    // 返回保存了头部之后的位置
    return (char*)ptr+PREFIX_SIZE;
#endif
}
```

redis将不同分配器的分配函数映射为zmalloc堆内存进行分配。其中包含HAVE_MALLOC_SIZE宏，这里为了判断返回的内存块中是否已经包含了头部即实际内存块的大小，来决定是否调用自定义的zmalloc_size函数。PREFIX_SIZE为最大内存寻址单位用于保存内存块的实际大小(即未进行内存对齐的大小)。再来看一下zmalloc_size函数是做什么。
### zmalloc_size
```c
#ifndef HAVE_MALLOC_SIZE
size_t zmalloc_size(void *ptr) {
    // 指针减去PREFIX_SIZE来定位到头部(保存内存块大小的位置)的首地址
    void *realptr = (char*)ptr-PREFIX_SIZE;
    // 读取size
    size_t size = *((size_t*)realptr);
    /* Assume at least that all the allocations are padded at sizeof(long) by
     * the underlying allocator. */
     // 对size进行内存对齐
    if (size&(sizeof(long)-1)) size += sizeof(long)-(size&(sizeof(long)-1));
    // 返回对齐后大小
    return size+PREFIX_SIZE;
}
// 实际给用户的大小需要减去PREFIX_SIZE
size_t zmalloc_usable(void *ptr) {
    return zmalloc_size(ptr)-PREFIX_SIZE;
}
#endif
```
如果返回的内存块中包含了大小，则直接从内存块的头部取出大小之后进行内存对齐，例如64为系统上sizeof(long)为8，则进行8字节对齐，之后加上PREFIX_SIZE，将用这个返回值来更新应用实际使用的内存。

    注意：这里为啥不直接使用前面传来的size而要从分配器返回回来的块里度呢，是因为分配器内部会做内存对齐，可能并不是之前的size了。

```c
#define update_zmalloc_stat_alloc(__n) do { \
    size_t _n = (__n); \
    // 内存对齐
    if (_n&(sizeof(long)-1)) _n += sizeof(long)-(_n&(sizeof(long)-1)); \
    atomicIncr(used_memory,__n); \
} while(0)
```
update_zmalloc_stat_alloc函数来更新内存对齐后实际使用的内存大小。宏定义中do...while是为了保持宏定义行为的统一，不管定义了多少分号和大括号扩展之后行为总是一致的。

既然有增加实际使用内存的，那么在释放内存时就有减少实际使用内存的函数。
```c
#define update_zmalloc_stat_free(__n) do { \
    size_t _n = (__n); \
    // 内存对齐
    if (_n&(sizeof(long)-1)) _n += sizeof(long)-(_n&(sizeof(long)-1)); \
    atomicDecr(used_memory,__n); \
} while(0)
```
进行内存对齐后将实际使用内存used_memory减去一定的数值。atomicIncr与atomicDecr内部都使用pthread_mutex_lock来避免共享区冲突，但redis为单线程模型并不会有锁？

## zcalloc函数
calloc与malloc函数都是用于分配内存的，但是calloc函数功能为指定分配n个大小为size的内存块，并将内存初始化为零值返回首地址。
```c
void *zcalloc(size_t size) {
    void *ptr = calloc(1, size+PREFIX_SIZE);

    if (!ptr) zmalloc_oom_handler(size);
#ifdef HAVE_MALLOC_SIZE
    update_zmalloc_stat_alloc(zmalloc_size(ptr));
    return ptr;
#else
    *((size_t*)ptr) = size;
    update_zmalloc_stat_alloc(size+PREFIX_SIZE);
    return (char*)ptr+PREFIX_SIZE;
#endif
}
```
可以看到，实际上在redis中，功能与zmalloc基本一致，不同的就是会将内存初始化为零值。

## zfree函数
free用来释放内存,同样会根据内存块中是否已经包含了块大小来分别进行处理。释放之前会讲指针定位到头部位置，读取内存块大小(未进行内存对齐的大小)，然后调用update_zmalloc_stat_free更新应用实际使用的内存大小，在这个函数里会对计算size内存对齐之后的大小并减去这一数值，最后通过free释放由malloc返回的地址。这里对应zmalloc中我们会对不包含内存大小的地址手动存入一个头部，然后将指针修改到(char*)ptr+PREFIX_SIZE，因为在释放时我们要相应的realptr = (char*)ptr-PREFIX_SIZE从而释放真是地址。
```c
void zfree(void *ptr) {
#ifndef HAVE_MALLOC_SIZE
    void *realptr;
    size_t oldsize;
#endif

    if (ptr == NULL) return;
#ifdef HAVE_MALLOC_SIZE
    update_zmalloc_stat_free(zmalloc_size(ptr));
    free(ptr);
#else
    realptr = (char*)ptr-PREFIX_SIZE;
    oldsize = *((size_t*)realptr);
    update_zmalloc_stat_free(oldsize+PREFIX_SIZE);
    free(realptr);
#endif
}
```

## zrealloc函数
realloc函数功能为为指针p重新分配大小为size的内存,如果重分配的size为0则意味着操作为释放内存；如果指针p为空，则意味着首次分配内存。接下来的操作即取出实际的地址位置(在手动添加头部时我们会移动指针，指向可以存用户数据的位置，现在需要减去PREFIX_SIZE得到分配器给我们的首地址)。然后重新分配size大小的空间，成功则修改当前应用的内存大小。
```c
void *zrealloc(void *ptr, size_t size) {
#ifndef HAVE_MALLOC_SIZE
    void *realptr;
#endif
    size_t oldsize;
    void *newptr;

    if (size == 0 && ptr != NULL) {
        zfree(ptr);
        return NULL;
    }
    if (ptr == NULL) return zmalloc(size);
#ifdef HAVE_MALLOC_SIZE
    oldsize = zmalloc_size(ptr);
    newptr = realloc(ptr,size);
    if (!newptr) zmalloc_oom_handler(size);

    update_zmalloc_stat_free(oldsize);
    update_zmalloc_stat_alloc(zmalloc_size(newptr));
    return newptr;
#else
    realptr = (char*)ptr-PREFIX_SIZE;
    oldsize = *((size_t*)realptr);
    newptr = realloc(realptr,size+PREFIX_SIZE);
    if (!newptr) zmalloc_oom_handler(size);

    *((size_t*)newptr) = size;
    update_zmalloc_stat_free(oldsize+PREFIX_SIZE);
    update_zmalloc_stat_alloc(size+PREFIX_SIZE);
    return (char*)newptr+PREFIX_SIZE;
#endif
}
```

## 碎片整理
redis的jemalloc分配器支持内存碎片整理，只是单纯的禁用了线程缓存直接从arena的bins中获取内存。

## 总结
redis的内存分配器基本就是三个主流内存分配的包装，内部没什么特别的东西，所以主要内存处理还是内存分配器在做，因此内存管理方式依赖于分配器的实现，默认采用jemalloc，个人认为jemalloc为ptmalloc与tcmalloc的结合版本。
- 主要结构类似于ptmalloc，结合了tcmalloc中的size_class不同大小维护不同的队列，支持线程缓冲区降低了锁冲突，与tcmalloc只支持一层线程缓冲不同，jemalloc在arena层面支持线程缓冲。
- 并且采用红黑树维护顺序，速度更快。

线程缓冲的空间越大，对应内存分配速度越快，但是也会产生更多的内存碎片。禁用线程缓冲之后可以减少内存碎片，redis默认单线程模型，所以没啥太大意义。

释放的内存不会立刻还给操作系统以备复用。