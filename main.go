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

func generateCallGraph(functions []FunctionInfo, codeFiles map[string]string) string {
	var nodeBuf strings.Builder

	nodeBuf.WriteString("digraph CallGraph {\n")
	nodeBuf.WriteString("  rankdir=TB;\n")
	nodeBuf.WriteString("  bgcolor=\"#ffffff\";\n")
	nodeBuf.WriteString("  fontname=\"Segoe UI\";\n")
	nodeBuf.WriteString("  fontsize=12;\n")
	nodeBuf.WriteString("  fontcolor=\"#000000\";\n\n")

	// 1. 保留所有函数
	keep := make(map[string]bool)
	for _, fn := range functions {
		keep[fn.Name] = true
	}

	// 2. 提取调用关系
	calls := extractFunctionCalls(functions, codeFiles)

	// 3. 分组
	fileGroups := make(map[string][]string)
	for _, fn := range functions {
		if !keep[fn.Name] {
			continue
		}
		file := fn.BelongsTo
		fileGroups[file] = append(fileGroups[file], fn.Name)
	}

	// 颜色方案
	nodeColors := []string{
		"#4a90e2", "#50c878", "#f3912", "#e74c3c", "#9b596", "#1abc9", "#f1c40f", "#e67e22",
	}

	clusterIdx := 0
	for file, fnames := range fileGroups {
		nodeBuf.WriteString(fmt.Sprintf("  subgraph cluster_%d {\n    label=\"%s\";\n    style=filled;\n    color=\"#ffffff\";\n    fillcolor=\"#ffffff\";\n    fontcolor=\"#000000\";\n    fontsize=11;\n    fontname=\"Segoe UI\";\n    penwidth=0;\n", clusterIdx, file))
		for i, fname := range fnames {
			shape := "box"
			nodeColor := nodeColors[i%len(nodeColors)]
			if fname == "main" {
				shape = "doubleoctagon"
				nodeColor = "#e67e22"
			}
			nodeBuf.WriteString(fmt.Sprintf("    func_%s [label=\"%s\", shape=%s, style=\"rounded,filled\", fillcolor=\"%s\", color=\"#ffffff\", penwidth=0, fontcolor=\"#ffffff\", fontsize=10, fontname=\"Segoe UI\"];\n", fname, fname, shape, nodeColor))
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
			color := "#e74c3c"
			style := "solid"
			if caller == callee || (isRecursive[caller] && isRecursive[callee]) {
				style = "dashed"
			}
			nodeBuf.WriteString(fmt.Sprintf("  func_%s -> func_%s [label=\"%s\", color=\"%s\", penwidth=1, style=%s];\n", caller, callee, label, color, style))
		}
	}

	nodeBuf.WriteString("}\n")
	return nodeBuf.String()
}
