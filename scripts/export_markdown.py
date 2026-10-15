# -*- coding: utf-8 -*-
"""
export_markdown.py: 将生成的全面架构 Word 文档导出为结构清晰的 Markdown 伴生文档
"""

import docx
import re

def export_docx_to_md(docx_path, md_path):
    doc = docx.Document(docx_path)
    lines = []
    
    lines.append("# 睿链进销存与往来管理系统\n")
    lines.append("## 系统架构、核心模块实现与全景架构演进优化设计说明书\n")
    lines.append("> **文档版本**：V1.2（含多租户与安全体系及未来架构优化蓝图）  ")
    lines.append("> **发布日期**：2026-10-15  ")
    lines.append("> **系统定位**：企业级采购、销售、库存及往来账务一体化管理平台  ")
    lines.append("> **安全等级**：企业级物理隔离与防篡改体系\n")
    lines.append("---\n")

    for block in doc.element.body:
        if block.tag.endswith('p'):
            p = docx.text.paragraph.Paragraph(block, doc)
            text = p.text.strip()
            if not text:
                continue
            if text in ["睿链进销存与往来管理系统", "系统架构、核心模块实现与全景架构演进优化设计说明书"]:
                continue
            
            # 标题识别
            if re.match(r'^(一|二|三|四|五|六|七|八|九|十|十一|十二|十三|十四|十五|十六|十七|十八|十九|二十)、', text):
                lines.append(f"\n## {text}\n")
            elif re.match(r'^\d+\.\d+\s', text):
                lines.append(f"\n### {text}\n")
            elif re.match(r'^\d+\.\d+\.\d+\s', text):
                lines.append(f"\n#### {text}\n")
            elif p.style.name == 'List Bullet':
                lines.append(f"- {text}")
            elif re.match(r'^\d+\.\s', text):
                lines.append(f"- {text}")
            else:
                lines.append(f"{text}\n")

        elif block.tag.endswith('tbl'):
            table = docx.table.Table(block, doc)
            if len(table.columns) == 1 and len(table.rows) == 1:
                cell_text = table.rows[0].cells[0].text.strip()
                if "【" in cell_text and "】" in cell_text and "\n" in cell_text and ("公式" in cell_text or "算法" in cell_text or "模式" in cell_text or "协议" in cell_text):
                    lines.append(f"```text\n{cell_text}\n```\n")
                elif cell_text.startswith("+--") or "CREATE TABLE" in cell_text or "SELECT" in cell_text or "type StorageAdapter" in cell_text or "data/" in cell_text:
                    lines.append(f"```text\n{cell_text}\n```\n")
                else:
                    quote_text = cell_text.replace("\n", "\n> ")
                    lines.append(f"> {quote_text}\n")
            elif len(table.rows) == 2 and len(table.columns) == 2 and "文档版本" in table.rows[0].cells[0].text:
                continue
            else:
                headers = [c.text.strip().replace('\n', ' ') for c in table.rows[0].cells]
                lines.append("| " + " | ".join(headers) + " |")
                lines.append("| " + " | ".join(["---"] * len(headers)) + " |")
                for r in table.rows[1:]:
                    vals = [c.text.strip().replace('\n', '<br>') for c in r.cells]
                    lines.append("| " + " | ".join(vals) + " |")
                lines.append("")

    with open(md_path, 'w', encoding='utf-8') as f:
        f.write("\n".join(lines) + "\n")
    print(f"[+] Markdown 文档已成功导出至: {md_path}")

if __name__ == '__main__':
    export_docx_to_md(
        "睿链进销存与往来管理系统_架构与实现设计说明书_V1.2_含多租户与安全体系.docx",
        "docs/睿链进销存与往来管理系统_架构与实现设计说明书_V1.2.md"
    )
