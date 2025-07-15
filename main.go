package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
)

// 2. 定义颜色组（从浅到深）
var colors = []string{"#b3c6ff", "#6699ff", "#3366cc", "#003399", "#001a4d"} // 5级蓝色

func main() {
	var cFiles []string
	root := "./"
	self := os.Args[0]
	codeFiles := make(map[string]string)

	// 递归扫描C文件
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if (strings.HasSuffix(path, ".c") || strings.HasSuffix(path, ".h")) && !strings.HasSuffix(path, self) {
			cFiles = append(cFiles, path)
		}
		return nil
	})

	// 解析C文件并生成UML
	allStructs := make(map[string]*StructInfo)
	var allFunctions []FunctionInfo

	for _, file := range cFiles {
		content, err := ioutil.ReadFile(file)
		if err != nil {
			fmt.Println("read file failed:", file)
			continue
		}
		structs, functions := parseCFile(string(content))

		// 合并结构体信息
		for name, info := range structs {
			if existing, exists := allStructs[name]; exists {
				// 如果结构体已存在，合并字段
				existing.Fields = append(existing.Fields, info.Fields...)
			} else {
				allStructs[name] = info
			}
		}

		// 合并函数信息
		allFunctions = append(allFunctions, functions...)
		// 保存源码内容，key为函数名
		for _, fn := range functions {
			codeFiles[fn.Name] = string(content)
		}
	}

	// 生成Graphviz dot
	dot := generateCallGraph(allFunctions, codeFiles)
	err := ioutil.WriteFile("uml.dot", []byte(dot), 0644)
	if err != nil {
		fmt.Println("write uml.dot failed:", err)
	}
	fmt.Println("Graphviz uml.dot generated")
}

type StructInfo struct {
	Name   string
	Fields []string
}

type FunctionInfo struct {
	Name       string
	ReturnType string
	Params     []string
	BelongsTo  string // 结构体名，如果是成员函数
}

func parseCFile(code string) (map[string]*StructInfo, []FunctionInfo) {
	parser := sitter.NewParser()
	parser.SetLanguage(c.GetLanguage())
	tree := parser.Parse(nil, []byte(code))
	rootNode := tree.RootNode()

	structs := make(map[string]*StructInfo)
	var functions []FunctionInfo

	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		// 结构体
		if n.Type() == "struct_specifier" {
			var structName string
			var fields []string
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "type_identifier" {
					structName = child.Content([]byte(code))
				}
				if child.Type() == "field_declaration_list" {
					for j := 0; j < int(child.NamedChildCount()); j++ {
						field := child.NamedChild(j)
						if field.Type() == "field_declaration" {
							typeNode := field.ChildByFieldName("type")
							nameNode := field.ChildByFieldName("declarator")
							if typeNode != nil && nameNode != nil {
								typeStr := typeNode.Content([]byte(code))
								nameStr := nameNode.Content([]byte(code))

								// 简化复杂类型，避免union、struct等嵌套
								if strings.Contains(typeStr, "union") || strings.Contains(typeStr, "struct") {
									fields = append(fields, fmt.Sprintf("complex_type %s", nameStr))
								} else {
									// 限制字段长度，避免过长的类型名
									if len(typeStr) > 50 {
										typeStr = typeStr[:47] + "..."
									}
									fields = append(fields, fmt.Sprintf("%s %s", typeStr, nameStr))
								}
							}
						}
					}
				}
			}
			if structName != "" {
				structs[structName] = &StructInfo{Name: structName, Fields: fields}
			}
		}
		// 函数
		if n.Type() == "function_definition" {
			decl := n.ChildByFieldName("declarator")
			typeNode := n.ChildByFieldName("type")
			var funcName, returnType string
			var params []string
			if typeNode != nil {
				returnType = typeNode.Content([]byte(code))
			}
			if decl != nil {
				// 获取函数名
				nameNode := decl.NamedChild(0)
				if nameNode != nil && nameNode.Type() == "identifier" {
					funcName = nameNode.Content([]byte(code))
				}
				// 获取参数
				paramList := decl.NamedChild(1)
				if paramList != nil && paramList.Type() == "parameter_list" {
					for i := 0; i < int(paramList.NamedChildCount()); i++ {
						param := paramList.NamedChild(i)
						if param.Type() == "parameter_declaration" {
							t := param.ChildByFieldName("type")
							n := param.ChildByFieldName("declarator")
							if t != nil && n != nil {
								params = append(params, fmt.Sprintf("%s %s", t.Content([]byte(code)), n.Content([]byte(code))))
							}
						}
					}
				}
			}
			if funcName != "" {
				functions = append(functions, FunctionInfo{
					Name:       funcName,
					ReturnType: returnType,
					Params:     params,
					BelongsTo:  "", // C 语言一般没有成员函数
				})
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(rootNode)
	return structs, functions
}

// 新增：函数调用关系分析
func extractFunctionCalls(functions []FunctionInfo, codeFiles map[string]string) map[string]map[string]int {
	calls := make(map[string]map[string]int)
	for _, fn := range functions {
		code := codeFiles[fn.Name]
		for _, callee := range functions {
			if fn.Name == callee.Name {
				continue
			}
			pattern := fmt.Sprintf("\\b%s\\s*\\(", callee.Name)
			re := regexp.MustCompile(pattern)
			count := len(re.FindAllStringIndex(code, -1)) // 这里 count 是 int
			if count > 0 {
				if calls[fn.Name] == nil {
					calls[fn.Name] = make(map[string]int)
				}
				calls[fn.Name][callee.Name] = count // 这里赋值为 int
			}
		}
	}
	return calls
}

// 字段类型归一化
func normalizeType(typeStr string) string {
	typeStr = strings.TrimSpace(typeStr)
	typeStr = strings.Trim(typeStr, "*[]")
	typeStr = strings.ReplaceAll(typeStr, "const ", "")
	for _, prefix := range []string{"struct ", "union ", "enum "} {
		if strings.HasPrefix(typeStr, prefix) {
			typeStr = strings.TrimPrefix(typeStr, prefix)
		}
	}
	return typeStr
}

func generateGraphviz(structs map[string]*StructInfo, functions []FunctionInfo, codeFiles map[string]string) string {
	var nodeBuf, relBuf strings.Builder
	nodeBuf.WriteString("digraph UML {\n  node [fontname=Helvetica];\n")

	// 只保留以core_开头的函数及其调用链
	keep := make(map[string]bool)
	for _, fn := range functions {
		keep[fn.Name] = true // 或根据你的需求过滤
	}

	// 统计最大调用次数
	calls := extractFunctionCalls(functions, codeFiles)
	maxCount := 1
	for _, calleeMap := range calls {
		for _, count := range calleeMap {
			if count > maxCount {
				maxCount = count
			}
		}
	}

	// 递归检测
	isRecursive := make(map[string]bool)
	var dfs func(start, curr string, visited map[string]bool) bool
	dfs = func(start, curr string, visited map[string]bool) bool {
		if curr == start && len(visited) > 0 {
			return true
		}
		if visited[curr] {
			return false
		}
		visited[curr] = true
		for next := range calls[curr] {
			if keep[next] && dfs(start, next, visited) {
				return true
			}
		}
		return false
	}
	for fname := range keep {
		visited := make(map[string]bool)
		if dfs(fname, fname, visited) {
			isRecursive[fname] = true
		}
	}

	// 结构体节点
	for _, s := range structs {
		if s.Name != "" && len(s.Fields) > 0 {
			nodeBuf.WriteString(fmt.Sprintf("  %s [shape=record, label=\"{%s|", s.Name, s.Name))
			for i, f := range s.Fields {
				if i > 0 {
					nodeBuf.WriteString("\\l")
				}
				nodeBuf.WriteString(f)
			}
			nodeBuf.WriteString("}\"];\n")
		}
	}
	// 结构体关系
	structNames := make(map[string]bool, len(structs))
	for name := range structs {
		structNames[name] = true
	}
	for _, s := range structs {
		for _, f := range s.Fields {
			parts := strings.Fields(f)
			if len(parts) < 2 {
				continue
			}
			typeStr := strings.Join(parts[:len(parts)-1], " ")
			fieldName := parts[len(parts)-1]
			baseType := normalizeType(typeStr)
			if structNames[baseType] && baseType != s.Name {
				lowerField := strings.ToLower(fieldName)
				typeIsPtr := strings.Contains(typeStr, "*")
				typeIsArr := strings.Contains(typeStr, "[") && strings.Contains(typeStr, "]")
				if (lowerField == "base" || lowerField == "parent" || lowerField == "super") && baseType != "" {
					relBuf.WriteString(fmt.Sprintf("  %s -> %s [arrowhead=onormal, label=\"继承\"]\n", s.Name, baseType))
				} else if typeIsPtr || typeIsArr {
					relBuf.WriteString(fmt.Sprintf("  %s -> %s [arrowhead=odiamond, label=\"聚合\"]\n", s.Name, baseType))
				} else {
					relBuf.WriteString(fmt.Sprintf("  %s -> %s [style=dashed, arrowhead=open, label=\"依赖\"]\n", s.Name, baseType))
				}
			}
		}
	}
	// 函数节点
	for _, fn := range functions {
		if fn.Name == "" {
			continue
		}
		nodeBuf.WriteString(fmt.Sprintf("  func_%s [shape=ellipse, label=\"%s\"];\n", fn.Name, fn.Name))
	}
	// 函数调用关系
	for caller, calleeMap := range calls {
		if !keep[caller] {
			continue
		}
		for callee, count := range calleeMap {
			if !keep[callee] {
				continue
			}
			label := fmt.Sprintf("%d", count)
			idx := 0
			if maxCount > 1 {
				idx = (count - 1) * (len(colors) - 1) / (maxCount - 1)
			}
			if idx < 0 {
				idx = 0
			}
			if idx >= len(colors) {
				idx = len(colors) - 1
			}
			color := colors[idx]
			if caller == callee || (isRecursive[caller] && isRecursive[callee]) {
				relBuf.WriteString(fmt.Sprintf("  func_%s -> func_%s [penwidth=1, label=\"%s\"]\n", caller, callee, label))
			} else {
				relBuf.WriteString(fmt.Sprintf("  func_%s -> func_%s [color=\"%s\", label=\"%s\"]\n", caller, callee, color, label))
			}
		}
	}
	nodeBuf.WriteString(relBuf.String())
	nodeBuf.WriteString("}\n")
	return nodeBuf.String()
}

func generateCallGraph(functions []FunctionInfo, codeFiles map[string]string) string {
	var nodeBuf, relBuf strings.Builder
	nodeBuf.WriteString("digraph CallGraph {\n  node [fontname=Helvetica];\n")

	// 1. 保留所有函数（调试用，后续可加过滤）
	keep := make(map[string]bool)
	for _, fn := range functions {
		keep[fn.Name] = true
	}
	fmt.Println("保留的函数数量：", len(keep))
	for k := range keep {
		fmt.Println("保留函数：", k)
	}

	// 2. 提取调用关系
	calls := extractFunctionCalls(functions, codeFiles)
	fmt.Println("调用关系数量：", len(calls))
	for caller, calleeMap := range calls {
		for callee, count := range calleeMap {
			fmt.Printf("%s -> %s: %d\n", caller, callee, count)
		}
	}

	// 3. 分组（可选，BelongsTo 为空时全部分到 other）
	fileGroups := make(map[string][]string)
	for _, fn := range functions {
		if !keep[fn.Name] {
			continue
		}
		file := fn.BelongsTo
		fileGroups[file] = append(fileGroups[file], fn.Name)
	}
	clusterIdx := 0
	for file, fnames := range fileGroups {
		nodeBuf.WriteString(fmt.Sprintf("  subgraph cluster_%d {\n    label=\"%s\";\n    style=filled;\n    color=\"#f0f0f0\";\n", clusterIdx, file))
		for _, fname := range fnames {
			shape := "ellipse"
			color := "#4682b4"
			if fname == "main" {
				shape = "doublecircle"
				color = "#e67e22"
			}
			nodeBuf.WriteString(fmt.Sprintf("    func_%s [label=\"%s\", shape=%s, style=filled, fillcolor=\"%s\"];\n", fname, fname, shape, color))
		}
		nodeBuf.WriteString("  }\n")
		clusterIdx++
	}

	// 4. 统计最大调用次数
	maxCount := 1
	for _, calleeMap := range calls {
		for _, count := range calleeMap {
			if count > maxCount {
				maxCount = count
			}
		}
	}
	fmt.Println("maxCount:", maxCount)

	// 5. 检查递归
	isRecursive := make(map[string]bool)
	var dfs func(start, curr string, visited map[string]bool) bool
	dfs = func(start, curr string, visited map[string]bool) bool {
		if curr == start && len(visited) > 0 {
			return true
		}
		if visited[curr] {
			return false
		}
		visited[curr] = true
		for next := range calls[curr] {
			if keep[next] && dfs(start, next, visited) {
				return true
			}
		}
		return false
	}
	for fname := range keep {
		visited := make(map[string]bool)
		if dfs(fname, fname, visited) {
			isRecursive[fname] = true
		}
	}

	// 6. 输出调用边
	for caller, calleeMap := range calls {
		if !keep[caller] {
			continue
		}
		for callee, count := range calleeMap {
			if !keep[callee] {
				continue
			}
			label := fmt.Sprintf("%d", count)
			idx := 0
			if maxCount > 1 {
				idx = (count - 1) * (len(colors) - 1) / (maxCount - 1)
			}
			if idx < 0 {
				idx = 0
			}
			if idx >= len(colors) {
				idx = len(colors) - 1
			}
			color := colors[idx]
			if caller == callee || (isRecursive[caller] && isRecursive[callee]) {
				relBuf.WriteString(fmt.Sprintf("  func_%s -> func_%s [penwidth=1, label=\"%s\"]\n", caller, callee, label))
			} else {
				relBuf.WriteString(fmt.Sprintf("  func_%s -> func_%s [color=\"%s\", label=\"%s\"];\n", caller, callee, color, label))
			}
		}
	}
	nodeBuf.WriteString(relBuf.String())
	nodeBuf.WriteString("}\n")
	return nodeBuf.String()
}
