package meter

import (
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/PuerkitoBio/goquery"
)

// Snapshot 保存页面快照，电量和单价放大一万倍，金额以分计，避免浮点误差。
type Snapshot struct {
	MeterID            string
	MeterName          string
	RemainingKWhX10000 int64
	RemainingCents     int64
	UnitPriceX10000    int64
}

var (
	ErrParse = errors.New("invalid meter page")
	digits   = regexp.MustCompile(`^[0-9]+$`)
	decimal  = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
)

// Parse 校验页面电表身份并提取名称、表号、电量、金额和单价；字段异常时拒绝整张快照。
func Parse(reader io.Reader, expectedID string) (Snapshot, error) {
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil || !digits.MatchString(expectedID) {
		return Snapshot{}, ErrParse
	}
	// 页面隐藏编号必须唯一且匹配请求目标，避免登录页或其他电表页面被误采集。
	id := doc.Find("#metid")
	pageID, ok := id.Attr("value")
	if !ok {
		pageID = id.Text()
	}
	if id.Length() != 1 || strings.TrimSpace(pageID) != expectedID {
		return Snapshot{}, ErrParse
	}
	name, err := field(doc, "表名称")
	if err != nil {
		return Snapshot{}, err
	}
	serial, err := field(doc, "表号")
	if err != nil {
		return Snapshot{}, err
	}
	// 各字段按固定精度转为整数，页面单位仅在对应字段的允许范围内剥离。
	var quantities [3]int64
	for i, spec := range []struct {
		label     string
		precision int
		units     []string
	}{
		{"剩余电量", 4, []string{"kWh", "度"}},
		{"剩余金额", 2, []string{"元"}},
		{"综合费用", 4, []string{"元/kWh", "元/度"}},
	} {
		value, err := field(doc, spec.label)
		if err != nil {
			return Snapshot{}, err
		}
		for _, unit := range spec.units {
			value = strings.TrimSpace(strings.TrimSuffix(value, unit))
		}
		if i == 1 {
			value = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(value, "¥"), "￥"))
		}
		quantities[i], err = ParseFixedDecimal(value, spec.precision)
		if err != nil {
			return Snapshot{}, ErrParse
		}
	}
	return Snapshot{serial, name, quantities[0], quantities[1], quantities[2]}, nil
}

// labelText 去除标签中的空白和末尾冒号，以匹配页面的不同排版。
func labelText(text string) string {
	return strings.TrimRight(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text), ":：")
}

// field 从唯一标签相邻位置读取值，并拒绝重复标签或跨字段误取值。
func field(doc *goquery.Document, label string) (string, error) {
	value := ""
	count := 0
	doc.Find("*").Each(func(_ int, s *goquery.Selection) {
		if labelText(s.Text()) != label {
			return
		}
		// 标签外层的包装节点不重复计数，只从最内层匹配开始定位。
		if s.Children().FilterFunction(func(_ int, child *goquery.Selection) bool {
			return labelText(child.Text()) == label
		}).Length() > 0 {
			return
		}
		count++
		for s.Length() > 0 {
			if next := s.Next(); next.Length() > 0 {
				// 相邻区域包含另一已知标签时停止，不能把下一字段的内容当作值。
				if next.Find("*").AddSelection(next).FilterFunction(func(_ int, node *goquery.Selection) bool {
					switch labelText(node.Text()) {
					case "表名称", "表号", "剩余电量", "剩余金额", "综合费用":
						return true
					}
					return false
				}).Length() > 0 {
					return
				}
				candidate := strings.TrimSpace(next.Text())
				if candidate != "" {
					value = candidate
				}
				break
			}
			parent := s.Parent()
			if labelText(parent.Text()) != label {
				break
			}
			// 只跨越纯文本包装；结构容器可能跨行，继续向上会扩大取值范围。
			switch goquery.NodeName(parent) {
			case "span", "label", "b", "strong", "em", "i":
			default:
				return
			}
			s = parent
		}
	})
	if value == "" || count != 1 {
		return "", ErrParse
	}
	return value, nil
}

// ParseFixedDecimal 将非负十进制文本按 precision 位小数缩放为 int64，拒绝有效小数超限和整数溢出。
func ParseFixedDecimal(value string, precision int) (int64, error) {
	if precision < 0 || !decimal.MatchString(value) {
		return 0, ErrParse
	}
	parts := strings.SplitN(value, ".", 2)
	fraction := ""
	// 末尾零不增加有效精度，允许 1.2300 按两位小数解析。
	if len(parts) == 2 {
		fraction = strings.TrimRight(parts[1], "0")
	}
	if len(fraction) > precision {
		return 0, ErrParse
	}
	integer := strings.TrimLeft(parts[0]+fraction+strings.Repeat("0", precision-len(fraction)), "0")
	if integer == "" {
		return 0, nil
	}
	// 交给整数解析检查范围，全程不经过浮点转换或四舍五入。
	n, err := strconv.ParseInt(integer, 10, 64)
	if err != nil {
		return 0, ErrParse
	}
	return n, nil
}
