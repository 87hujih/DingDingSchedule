# 考勤文本接口文档

## 接口概述

新增的考勤文本接口用于生成简洁的考勤文本格式，方便复制到群里发送。

## 接口信息

**接口路径**: `GET /api/attendance/record/text`

**功能**: 获取格式化的考勤文本，只包含人名和考勤状态

**权限**: 需要登录认证

## 请求参数

| 参数名 | 类型 | 必填 | 说明 | 示例 |
|--------|------|------|------|------|
| date | string | 是 | 考勤日期 | 2026-02-05 |
| week | int | 是 | 周次 | 3 |
| section | int | 是 | 节次 | 1 |
| dept_ids | string | 否 | 部门ID列表（逗号分隔） | 1,2,3 |

## 响应格式

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "title": "📅 2026-02-05 第3周 第1节 考勤",
    "statistics": "📊 应到30人，正常打卡25人，请假3人，未到2人",
    "content": [
      "✅ 正常打卡(25人)：\n张三、李四、王五、赵六、孙七、周八、吴九、郑十、钱一、陈二、褚三、卫四、蒋五、沈六、韩七、杨八、朱九、秦十、尤一、许二、何三、吕四、施五、张六、孔七",
      "🏥 请假(3人)：\n刘一（病假）、陈二（事假）、林三（年假）",
      "❌ 未到(2人)：\n王四、李五"
    ],
    "full_text": "📅 2026-02-05 第3周 第1节 考勤\n📊 应到30人，正常打卡25人，请假3人，未到2人\n\n✅ 正常打卡(25人)：\n张三、李四、王五...\n\n🏥 请假(3人)：\n刘一（病假）、陈二（事假）、林三（年假）\n\n❌ 未到(2人)：\n王四、李五"
  }
}
```

## 响应字段说明

| 字段名 | 类型 | 说明 |
|--------|------|------|
| title | string | 考勤标题（包含日期、周次、节次） |
| statistics | string | 统计信息（应到、正常打卡、请假、未到人数） |
| content | array | 分类人员列表（每个元素是一个分类的文本） |
| full_text | string | 完整的格式化文本（可直接复制） |

## 使用示例

### 请求示例

```bash
curl -X GET "http://localhost:8080/api/attendance/record/text?date=2026-02-05&week=3&section=1" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

### 前端使用示例

```javascript
// 获取考勤文本
fetch('/api/attendance/record/text?date=2026-02-05&week=3&section=1', {
  headers: {
    'Authorization': 'Bearer ' + token
  }
})
.then(res => res.json())
.then(data => {
  if (data.code === 0) {
    // 方式1: 直接使用完整文本
    const text = data.data.full_text;

    // 方式2: 使用结构化数据自定义展示
    console.log(data.data.title);
    console.log(data.data.statistics);
    data.data.content.forEach(line => console.log(line));

    // 一键复制功能
    navigator.clipboard.writeText(text);
  }
});
```

## 文本格式说明

生成的文本使用以下格式：

1. **标题行**: 📅 日期 第X周 第X节 考勤
2. **统计行**: 📊 应到X人，正常打卡X人，请假X人，未到X人
3. **分类列表**:
   - ✅ 正常打卡(X人)：姓名1、姓名2、姓名3...
   - 🏥 请假(X人)：姓名1（请假类型）、姓名2（请假类型）...
   - ❌ 未到(X人)：姓名1、姓名2、姓名3...

## 注意事项

1. 人员姓名使用中文顿号（、）分隔，一行显示
2. 请假人员会显示请假类型（如：病假、事假、年假等）
3. 如果某个分类没有人员，则不显示该分类
4. 支持部门过滤，只显示指定部门的考勤数据
5. 接口会实时计算考勤数据，不依赖已保存的快照

## 与其他接口的区别

| 接口 | 用途 | 数据来源 | 返回格式 |
|------|------|----------|----------|
| `/api/attendance/record/detail` | 查看详细考勤数据 | 实时计算 | JSON结构化数据 |
| `/api/attendance/record/snapshot` | 查看历史快照 | 数据库 | JSON结构化数据 |
| `/api/attendance/record/text` | 复制到群里 | 实时计算 | 格式化文本 |

## 实现细节

- **DTO**: `internal/dto/attendance_record.go`
  - `AttendanceTextRequest`: 请求DTO
  - `AttendanceTextResponse`: 响应DTO

- **Service**: `internal/service/attendance_record_service.go`
  - `GetAttendanceText()`: 获取考勤文本
  - `formatAttendanceText()`: 格式化文本

- **Handler**: `internal/handler/attendance_record_handler.go`
  - `GetAttendanceText()`: HTTP处理器

- **Router**: `internal/app/routers_attendance.go`
  - 路由注册
