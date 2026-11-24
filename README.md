# tempora — 查询引擎时间类型与函数实现

用 Go 重写查询引擎里所有时间相关的类型和函数。**时区规则(偏移、夏令时、历史变更)只来自 IANA tzdata
(经标准库 `time` 读取);日期运算、类型转换、解析、格式化、分组语义全部在本仓库内自行实现。**

## 1. 四种核心类型

| 类型 | 含义 | 内部表示 | 是不是"一个时刻" |
|---|---|---|---|
| `Timestamp` | 带时区的绝对时刻 | UTC `time.Time`,单调时钟剥离 | 是 |
| `LocalDateTime` | 不带时区的墙上时间 | 年/月/日/时/分/秒/纳秒 | 否 |
| `Date` | 纯日历日期 | 年/月/日 | 否 |
| `Interval` | 时间间隔 | `Months` / `Days` / `Nanoseconds` 三段 | — |

三段式 `Interval` 刻意**不互相归一化**:1 个月不是固定天数,1 天在 DST 日不是固定纳秒数。

- `Timestamp` 只存 UTC,所以同一时刻在任何时区写法下 `Equal`/`Compare`/`JoinKey` 恒相等 ——
  连接键是 UTC 纳秒字符串,与展示时区无关。
- `LocalDateTime` 没有时区,不能直接和 `Timestamp` 比较;必须经 `Session` 做一次带策略的显式转换。
- 会话 `Session` 携带查询级时区和 DST 策略,没有全局可变时区;两个不同会话可在同一进程并存,
  对应两条不同 `SET TIME ZONE` 的连接。

### 类型转换规则

| 从 → 到 | 规则 |
|---|---|
| `Timestamp → LocalDateTime` | 总会成功:在会话时区渲染墙钟 |
| `Timestamp → Date` | 取会话时区的日历日期 |
| `LocalDateTime → Timestamp` | **非全函数**,按 gap/overlap 策略处理(见下) |
| `Date → Timestamp` | 取本地午夜,同样走策略 |
| `LocalDateTime → Date` | 丢弃时分秒 |
| 字符串 → 各类型 | 严格校验,非法日期(`2023-02-29`、13 月、24 时)直接报错,不做静默归一 |

## 2. 夏令时间隙(gap)与重复(overlap)

引擎不依赖 `time.Date` 对不存在时间的隐式行为,而是:

1. 从一个候选偏移出发,在其两侧 ±27h 反复探测,定点扩散出该墙钟附近**所有**生效偏移
   (覆盖 1 小时常规跳转、Lord Howe 的 30 分钟跳转、Pacific/Apia 的 24 小时日界线跳转);
2. 用每个偏移把输入墙钟解释成候选时刻,再投影回该时区墙钟,**字段完全相等才接受**;
3. 回投命中 1 个 = 正常,2 个 = overlap(按时刻排序),0 个 = gap;
4. gap 的精确切换时刻由两侧候选时刻**二分查找**到秒。

策略可在会话上配置,默认值如下:

| 情况 | 策略 | 行为 |
|---|---|---|
| gap(春季跳时,如纽约 02:00→03:00) | `GapForward`(默认) | 保留在 gap 内的相对位置整体前移:`02:30 → 03:30` |
| | `GapBackward` | 墙钟整体回退一个 gap 长度:`02:30 → 01:30` |
| | `GapReject` | 返回 `ErrGap`,报表脊柱用它区分"真失败"和"空 gap 桶" |
| overlap(秋季回退,01:00→02:00 重复) | `OverlapFirst`(默认) | 取第一次(夏令时大偏移上的较早时刻) |
| | `OverlapLast` | 取第二次(冬令时小偏移上的较晚时刻) |
| | `OverlapReject` | 返回 `ErrOverlap` |

`Session.ResolveWall` 返回不带策略的完整分类结果,`GapWindow()`/`OverlapWindow()`
给出受影响的墙钟区间(纽约春令 `02:00→03:00`,秋令 `01:00→02:00`)。

## 3. 时间间隔的三种语义

应用顺序固定:先月,再天,最后纳秒。

| 分量 | 作用在 `Timestamp`(经会话) | 作用在 `LocalDateTime` |
|---|---|---|
| 月 | 目标月同日,月末 clamp 后重新按策略解析 | 纯日历字段,月末 clamp |
| 天 | 墙钟字段 +24h,跨 DST 实际是 **23 或 25 小时** | 纯日历 +1 天 |
| 纳秒 | 精确时刻平移,**DST 免疫** | 墙钟加减,自然向日进位 |

月末规则是确定的、无"记忆"的 clamp:

```
2023-01-31 + 1月 = 2023-02-28
2023-02-28 + 1月 = 2023-03-28   // 不是 3 月 3 号
2024-01-31 + 1月 = 2024-02-29   // 闰年
```

`ParseInterval` 支持 ISO-8601:`P1Y2M10DT2H30M`、`PT0.5S`、`P2W`(=14 天,不可与其他分量混用)。

## 4. 截断、分组与报表脊柱

所有截断(`TruncHour/Day/Week/Month/Year`)都按**会话时区的本地日历**进行;周一是 ISO 周起点。

`HourlySpine(start, end)` 枚举区间内**每一个墙钟小时**,这是报表正确性的关键:

- 春季跳时日:24 个墙钟标签,其中 1 个是 `BucketGap` —— 行保留,`Start/End` 为空,
  聚合后 `Has=false`、度量为 `null`,**绝不凭空造数**;真实时长合计 23 小时。
- 秋季回退日:重复的墙钟小时产出**两行**(`BucketOverlapFirst/Last`),共 25 个桶,
  各有互不相交的时刻区间,事件按 `[Start, End)` 半开区间归属,**两次出现不合并、不丢事件**;
  真实时长合计 25 小时。
- `AggregateHourly` 用时刻包含关系而非截断值分桶,保证 overlap 的两个桶各自收到自己的事件。

`DailySpine` 同理枚举本地日历日。

## 5. 比较、排序与连接键

- `Timestamp.Equal/Before/After/Compare` 全部基于 UTC 时刻;`JoinKey()` 是 UTC 纳秒。
  纽约的 `08:00 EDT` 与上海的 `20:00 CST` 是同一时刻 → 比较相等、连接键相同。
- `LocalDateTime.CompareLocal` 只比日历字段;跨类型比较必须先经会话显式转换,引擎不偷偷替你选时区。

## 6. 字符串解析与格式化

手写扫描器,不委托 `time.Parse`。支持:

```
2024-03-10                      -> Date
2024/03/10                      -> Date
2024-03-10T02:30:00             -> LocalDateTime(空格可代替 T,可带 9 位以内小数)
2024-03-10T02:30:00Z            -> Timestamp(UTC)
2024-03-10T02:30:00+08:00 / +0800 / +08
2024-03-10T02:30:00[America/New_York]   -> IANA(ISO-8601-2 方括号写法)
2024-03-10T02:30:00 America/New_York    -> IANA(空格写法)
```

- 偏移范围校验 ±24h;`CST` 这类有歧义的缩写一律拒绝,要求给显式偏移或 IANA 名。
- 带 IANA 名的解析会应用指定会话的 gap/overlap 策略(`ParseTimestampSession`)。
- 格式化提供 strftime 风格 token(`%Y %m %d %H %M %S %f %z %:z %Z %b %B %a %A`)和
  一键 ISO 辅助 `ISODate/ISOLocal/ISOTimestamp`;时区 token 只允许用于 `Timestamp`。

## 7. 测试方法:逐条对拍权威库

测试文件内嵌 `time/tzdata`,结果不依赖宿主机 zoneinfo 版本。标准库 `time` 是权威参照,
但 `dst_test.go` 里的参照解析是**另写的一份独立实现**(同样的候选-回投思路,但与 `session.go`
不共享代码),避免同源 bug 互相背书。

覆盖内容:

- 11 个时区 × 跳转日 + 30 个确定性伪随机日期,每 30 分钟采样,逐条核对 kind、两个边界时刻、两侧偏移;
- 纽约 2024 春令/秋令的全部策略(forward/backward/reject、first/last/reject);
- Lord Howe 30 分钟跳转、Pacific/Apia 2011 整日消失(24/24 gap)、Asia/Tehran/上海历史规则变更;
- 月末 clamp、闰年、自定义日历算法与 stdlib 的星期/日期对拍(2020–2030 全覆盖);
- 报表脊柱:春令 24 行(1 gap+23 真实,合计 23h)、秋令 25 行(两个 01:00,合计 25h),
  事件条数与度量合计精确核对;
- 跨时区相等性、解析非法值报错、格式化往返。

```bash
go test -race -count=1 ./...
go test -cover ./...        # 约 80% 语句覆盖
```

## 8. HTTP 服务与可视化页面

```bash
go run ./cmd/server -addr :8080
```

打开 http://localhost:8080,三个面板:

1. **同一时刻各时区写法** —— 选基准时区和墙上时间,对比多个 IANA 区,所有行的 Unix 纳秒(连接键)相同;
2. **DST 切换日时间轴** —— 24 个墙钟单元格,gap 红色(不存在)、overlap 黄色(重复),并给出精确切换点;
3. **按小时汇总报表** —— 完整脊柱、行数、事件总条数、度量合计,gap 行显示 `null(不造数)`。

API:

| 路径 | 参数 |
|---|---|
| `GET /api/zones` | — |
| `GET /api/instant` | `baseZone`, `wall`, `zones`(逗号分隔) |
| `GET /api/timeline` | `zone`, `date=YYYY-MM-DD` |
| `GET /api/report` | `zone`, `date=YYYY-MM-DD` |

## 包结构

```
types.go       四种核心类型与校验
civil.go       自研公历算法(Hinnant civil-days)、月末 clamp、墙钟加减
interval.go    三段式 Interval 与 ISO-8601 解析
session.go     会话、gap/overlap 策略、ResolveWall 精确分类与转换
arithmetic.go  月/天/纳秒在时刻与墙钟上的不同语义
truncate.go    截断、小时/日脊柱、泛型聚合
parse.go       手写时间解析(偏移、IANA、严格非法值报错)
format.go      手写格式化
transition.go  切换点查找与本地时间轴
cmd/server/    HTTP API 与单页可视化
```
