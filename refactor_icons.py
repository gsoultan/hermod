import os
import re

def refactor_icons(file_path):
    with open(file_path, 'r', encoding='utf-8') as f:
        content = f.read()
    
    # Matches both single and multiline imports
    pattern = re.compile(r'import\s+\{([^}]+)\}\s+from\s+[\'"]@tabler/icons-react[\'"];?', re.MULTILINE)
    
    new_content = content
    matches = list(pattern.finditer(content))
    if not matches:
        return False

    # Work backwards to not mess up indices
    for match in reversed(matches):
        match_str = match.group(0)
        icons_block = match.group(1)
        icons = [i.strip() for i in icons_block.split(',')]
        icons = [i for i in icons if i]
        
        replacement = []
        for icon in icons:
            if ' as ' in icon:
                orig, alias = icon.split(' as ')
                orig = orig.strip()
                alias = alias.strip()
                replacement.append(f"import {alias} from '@tabler/icons-react{orig}.mjs';")
            else:
                replacement.append(f"import {icon} from '@tabler/icons-react{icon}.mjs';")
        
        new_content = new_content[:match.start()] + "\n".join(replacement) + new_content[match.end():]

    if new_content != content:
        with open(file_path, 'w', encoding='utf-8') as f:
            f.write(new_content)
        return True
    return False

count = 0
for root, dirs, files in os.walk('ui/src'):
    for file in files:
        if file.endswith('.tsx') or file.endswith('.ts'):
            path = os.path.join(root, file)
            if refactor_icons(path):
                print(f"Refactored {path}")
                count += 1
print(f"Total refactored: {count}")
