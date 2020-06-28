# redis-zipmap

相关文件
- zipmap.h
- zipmap.c

## 数据结构
zipmap是一种采用压缩编码的map。

```c
 * <zmlen><len>"foo"<len><free>"bar"<len>"hello"<len><free>"world"
```

zmlen为一个字节的zipmap长度，最多表示254，超过254之后，这个字节就没用了，这时获取map长度就要遍历了O(N)

len为key/val的长度，长度不超过253时只占一个字节，254表示后面有一个四字节的无符号整数，255表示结尾

free表示后面有几个没用的字节，修改val时，可能变短了，就有空闲了。这个字节表示的空间可以让接下来的操作利用。

free是一个无符号8位整数，当更新操作之后产生的空闲字节超过了限制，会重新分配空间来节省内存。

```c
 * "\x02\x03foo\x03\x00bar\x05hello\x05\x00world\xff"
```
查找时间复杂度为O(N)。

操作比较简单，顺序遍历即可，空间不足或者空闲空间过大会触发resize。