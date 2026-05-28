package browser

const browserSnapshotRuntimeScript = `(() => {
  const captureSnapshotRows = function captureSnapshotRows() {
    const normalize = (value, max = 200) => String(value || '').replace(/\s+/g, ' ').trim().slice(0, max);
    const isVisible = (el) => {
        const rect = el.getBoundingClientRect();
        const style = window.getComputedStyle(el);
        return (style.display !== 'none' &&
            style.visibility !== 'hidden' &&
            style.opacity !== '0' &&
            rect.width > 0 &&
            rect.height > 0);
    };
    const isEnabled = (el) => !el.hasAttribute('disabled') && el.getAttribute('aria-disabled') !== 'true';
    const cssPath = (el) => {
        const parts = [];
        let current = el;
        while (current && current.nodeType === Node.ELEMENT_NODE && parts.length < 8) {
            let selector = current.tagName.toLowerCase();
            if (current.id) {
                selector += ` + "`" + `#${CSS.escape(current.id)}` + "`" + `;
                parts.unshift(selector);
                break;
            }
            let nth = 1;
            let sib = current.previousElementSibling;
            while (sib) {
                if (sib.tagName === current.tagName)
                    nth += 1;
                sib = sib.previousElementSibling;
            }
            selector += ` + "`" + `:nth-of-type(${nth})` + "`" + `;
            parts.unshift(selector);
            current = current.parentElement;
        }
        return parts.join(' > ');
    };
    const roleOf = (el) => normalize(el.getAttribute('role') || el.tagName.toLowerCase(), 80);
    const textOf = (el) => normalize(el.innerText || el.textContent || '', 200);
    const nameOf = (el) => normalize(el.getAttribute('aria-label') ||
        el.getAttribute('placeholder') ||
        el.getAttribute('title') ||
        el.innerText ||
        el.value ||
        '', 120);
    const groupOf = (el) => {
        const tag = el.tagName.toLowerCase();
        if (tag === 'a')
            return 'links';
        if (tag === 'button')
            return 'buttons';
        if (tag === 'input')
            return 'inputs';
        if (tag === 'textarea')
            return 'textareas';
        if (tag === 'select')
            return 'selects';
        if (tag === 'area')
            return 'areas';
        const role = normalize(el.getAttribute('role'), 40);
        if (role)
            return 'customs';
        return 'customs';
    };
    const actionableSeen = new Set();
    const out = [];
    const actionableNodes = Array.from(document.querySelectorAll('a,button,input,textarea,select,area,summary,[role],[tabindex]'));
    for (const el of actionableNodes) {
        if (!isVisible(el))
            continue;
        if (!isEnabled(el))
            continue;
        const selector = cssPath(el);
        if (!selector || actionableSeen.has(selector))
            continue;
        actionableSeen.add(selector);
        out.push({
            selector,
            group: groupOf(el),
            role: roleOf(el),
            name: nameOf(el),
            text: textOf(el),
            tagName: el.tagName.toLowerCase(),
            href: normalize(el.getAttribute('href') || '', 200),
            value: normalize(el.value || '', 200),
            placeholder: normalize(el.getAttribute('placeholder') || '', 120),
            textLength: textOf(el).length
        });
    }
    const textCandidates = Array.from(document.querySelectorAll('p,h1,h2,h3,h4,h5,h6,article,section,div,span,li,blockquote,pre,code'));
    const textRows = [];
    for (const el of textCandidates) {
        if (!isVisible(el))
            continue;
        const selector = cssPath(el);
        if (!selector)
            continue;
        const text = textOf(el);
        if (text.length < 6)
            continue;
        if (el.querySelector('a,button,input,textarea,select,area,[role],[tabindex]'))
            continue;
        textRows.push({
            selector,
            group: 'texts',
            role: '',
            name: '',
            text,
            tagName: el.tagName.toLowerCase(),
            href: '',
            value: '',
            placeholder: '',
            textLength: text.length
        });
    }
    textRows.sort((a, b) => a.selector.length - b.selector.length);
    const dedupedTexts = [];
    for (const row of textRows) {
        let nested = false;
        for (const kept of dedupedTexts) {
            if (!row.selector.startsWith(kept.selector))
                continue;
            try {
                const parentEl = document.querySelector(kept.selector);
                const childEl = document.querySelector(row.selector);
                if (parentEl && childEl && parentEl !== childEl && parentEl.contains(childEl)) {
                    nested = true;
                    break;
                }
            }
            catch {
            }
        }
        if (!nested)
            dedupedTexts.push(row);
    }
    return out.concat(dedupedTexts);
};
  const inferGroup = function inferGroup(row) {
    const explicit = row.group.trim().toLowerCase();
    if (['links', 'buttons', 'inputs', 'textareas', 'selects', 'areas', 'customs', 'texts'].includes(explicit)) {
        return explicit;
    }
    switch (row.tagName.trim().toLowerCase()) {
        case 'a':
            return 'links';
        case 'button':
            return 'buttons';
        case 'input':
            return 'inputs';
        case 'textarea':
            return 'textareas';
        case 'select':
            return 'selects';
        case 'area':
            return 'areas';
        default:
            if (row.role.trim() !== '') {
                return 'customs';
            }
            return 'texts';
    }
};
  const buildPageSnapshot = function buildPageSnapshot(rows, url, title) {
    const groups = {};
    const refs = {};
    let elementIndex = 0;
    let textIndex = 0;
    const addRow = (group, columns, values) => {
        const table = groups[group] ?? { columns: [], rows: [] };
        if (table.columns.length === 0) {
            table.columns = columns;
        }
        table.rows.push(values);
        groups[group] = table;
    };
    for (const row of rows) {
        const group = row.group.trim() || inferGroup(row);
        let kind = 'element';
        let ref;
        if (group === 'texts') {
            textIndex += 1;
            ref = ` + "`" + `t${textIndex}` + "`" + `;
            kind = 'text';
        }
        else {
            elementIndex += 1;
            ref = ` + "`" + `e${elementIndex}` + "`" + `;
        }
        refs[ref] = {
            ref,
            kind,
            role: row.role,
            name: row.name,
            tagName: row.tagName,
            text: row.text,
            selector: row.selector
        };
        const tagName = row.tagName.toUpperCase();
        switch (group) {
            case 'links':
                addRow(group, ['ref', 'tag', 'text', 'href'], [ref, tagName, row.text, row.href]);
                break;
            case 'buttons':
                addRow(group, ['ref', 'tag', 'text'], [ref, tagName, row.text]);
                break;
            case 'inputs':
            case 'textareas':
                addRow(group, ['ref', 'tag', 'value', 'placeholder'], [ref, tagName, row.value, row.placeholder]);
                break;
            case 'selects':
                addRow(group, ['ref', 'tag', 'value'], [ref, tagName, row.value]);
                break;
            case 'areas':
                addRow(group, ['ref', 'tag', 'text', 'href'], [ref, tagName, row.text, row.href]);
                break;
            case 'customs':
                addRow(group, ['ref', 'tag', 'role', 'text'], [ref, tagName, row.role, row.text]);
                break;
            case 'texts':
                addRow(group, ['ref', 'tag', 'text', 'textLength'], [ref, tagName, row.text, row.textLength]);
                break;
        }
    }
    return {
        page: {
            url,
            title,
            groups
        },
        refs
    };
};
  const rows = captureSnapshotRows();
  return buildPageSnapshot(rows, location.href, document.title);
})()`
