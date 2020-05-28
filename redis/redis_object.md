# redis中的对象系统
相关文件
- object.c
- server.h

redis中的基础数据结构并不直接提供给指令使用，在这两者之间还有一层对象系统，指令直接操作的是object，object可以由一种或多种数据结构实现。