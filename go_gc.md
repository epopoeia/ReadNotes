1. collector 有吞吐限制1-2m/ms
2. 尽量在栈上分配
3. goroutine太大辅助标记过程会卡住，递归会产生很大栈帧，尽量少分裂routine，辅助标记最少扫面64kb内存。每次扫描完给一个信贷额度n*64kb
尽量用一个routine

4. gogc只有一个参数控制gc，100；gc保证finalizer对象存活一轮

5. hack方式，先申请一块内存，keeplieve，只是节省cpu，防止辅助标记过于频繁

## go 分配器

分区适应算法，66个sizeclass

每一个系统线程有一个mcache，每个mcache里面存由sizeclass缓存