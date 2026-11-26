/* Open Go Panel code editor. The native form remains the save mechanism. */
(() => {
  'use strict';
  const CM = window.CodeMirror;
  if (!CM) return;
  const modes = {
    text: 'text/plain', html: 'htmlmixed', json: 'application/json',
    js: 'javascript', ts: 'application/typescript', css: 'css', xml: 'xml',
    php: 'application/x-httpd-php', go: 'go', python: 'python', shell: 'shell',
    yaml: 'yaml', sql: 'text/x-sql', markdown: 'markdown', ini: 'properties'
  };
  function detect(name) {
    name = name.toLowerCase();
    if (name === '.env' || name.startsWith('.env.') || /(?:^|\/)(?:dockerfile|makefile)$/.test(name)) return name.includes('env') ? 'ini' : 'text';
    const ext = name.split('.').pop();
    return ({html:'html',htm:'html',json:'json',jsonc:'js',js:'js',mjs:'js',cjs:'js',ts:'ts',css:'css',xml:'xml',svg:'xml',php:'php',go:'go',py:'python',sh:'shell',bash:'shell',yaml:'yaml',yml:'yaml',sql:'sql',md:'markdown',ini:'ini',conf:'ini',properties:'ini'})[ext] || 'text';
  }
  const encoder = new TextEncoder();
  function encodeBase64(value) {
    const bytes = encoder.encode(value);
    let text = '';
    for (let i=0; i<bytes.length; i+=8192) text += String.fromCharCode(...bytes.subarray(i,i+8192));
    return btoa(text);
  }

  // Validate JSON, then change whitespace only. JSON.parse/stringify would
  // silently round integers, discard duplicate keys and change string escapes.
  function formatJSON(source, max) {
    JSON.parse(source);
    const tokens = source.match(/"(?:\\.|[^"\\])*"|[{}\[\],:]|[^\s{}\[\],:"]+/g) || [];
    const out = [];
    let length = 0, depth = 0;
    function append(value) {
      length += value.length;
      if (length > max) throw new Error('Formatted JSON exceeds the editor size limit.');
      out.push(value);
    }
    function line() {
      if (depth > 256) throw new Error('JSON nesting is too deep to format.');
      append('\n' + '  '.repeat(depth));
    }
    for (let i=0; i<tokens.length; i++) {
      const token = tokens[i], prev = tokens[i-1], next = tokens[i+1];
      if (token === '{' || token === '[') {
        append(token); depth++;
        if (next !== '}' && next !== ']') line();
      } else if (token === '}' || token === ']') {
        depth--;
        if (prev !== '{' && prev !== '[') line();
        append(token);
      } else if (token === ',') { append(','); line(); }
      else if (token === ':') append(': ');
      else append(token);
    }
    if (source.endsWith('\n')) append('\n');
    const result = out.join('');
    if (encoder.encode(result).length > max) throw new Error('Formatted JSON exceeds the editor size limit.');
    return result;
  }

  let active = null;
  function initialize(form) {
    if (form.dataset.editorReady === '1') return;
    const source = form.querySelector('textarea[name=content]');
    const tools = form.querySelector('[data-editor-tools]');
    if (!source || !tools) return;
    const mode = tools.querySelector('[data-editor-mode]');
    const status = form.querySelector('[data-editor-status]');
    const size = form.querySelector('[data-editor-size]');
    const filename = form.elements.name;
    const max = Number(source.getAttribute('maxlength')) || 2097152;
    const eol = source.dataset.eol === 'crlf' ? '\r\n' : source.dataset.eol === 'cr' ? '\r' : '\n';
    const saveButton = form.querySelector('button[type=submit]');
    let submitting = false, manualMode = false;
    mode.value = detect(filename?.value || '');
    const cm = CM.fromTextArea(source, {
      mode:modes[mode.value], lineNumbers:true, lineWrapping:false, matchBrackets:true,
      autoCloseBrackets:true, indentUnit:2, tabSize:4, indentWithTabs:false,
      viewportMargin:10, inputStyle:'textarea',
      extraKeys:{
        'Ctrl-S': () => form.requestSubmit(saveButton),
        'Cmd-S': () => form.requestSubmit(saveButton),
        'Tab': 'indentMore', 'Shift-Tab': 'indentLess'
      }
    });
    const initial = cm.changeGeneration(true);
    const value = () => cm.getValue(eol);
    const dirty = () => !cm.isClean(initial);
    const message = (text, error=false) => {
      status.textContent = text;
      status.classList.toggle('editor-error', error);
    };
    function update() {
      const bytes = encoder.encode(value()).length;
      const cursor = cm.getCursor();
      size.textContent = 'Ln ' + (cursor.line+1) + ', Col ' + (cursor.ch+1) + ' · ' + bytes.toLocaleString() + ' B';
      saveButton.disabled = bytes > max;
      if (bytes > max) message('File exceeds the 2 MiB editor limit.',true);
      else if (!status.classList.contains('editor-error') || status.textContent.includes('editor limit')) message(dirty() ? 'Unsaved changes' : 'No changes');
    }
    let timer;
    cm.on('change', () => {
      clearTimeout(timer);
      timer = setTimeout(update, 100);
    });
    cm.on('cursorActivity', () => { clearTimeout(timer); timer = setTimeout(update,100); });
    mode.addEventListener('change', () => { manualMode = true; cm.setOption('mode', modes[mode.value]); });
    if (filename?.type !== 'hidden') filename?.addEventListener('input', () => {
      if (!manualMode) { mode.value = detect(filename.value); cm.setOption('mode', modes[mode.value]); }
    });
    tools.querySelector('[data-editor-wrap]').addEventListener('change', event => cm.setOption('lineWrapping',event.target.checked));
    tools.addEventListener('click', event => {
      const button = event.target.closest('button[data-editor-action]');
      if (!button) return;
      const action = button.dataset.editorAction;
      if (action === 'find') cm.execCommand('find');
      else if (action === 'replace') cm.execCommand('replace');
      else if (action === 'undo') cm.undo();
      else if (action === 'redo') cm.redo();
      else if (action === 'format') {
        try {
          if (mode.value === 'json') {
            const formatted = formatJSON(cm.getValue(),max);
            if (formatted !== cm.getValue()) cm.replaceRange(formatted,{line:0,ch:0}, {line:cm.lastLine(),ch:cm.getLine(cm.lastLine()).length},'+format');
            message('JSON formatted without changing values.');
          } else {
            // Reindent only existing lines. Never parse or render HTML.
            if (encoder.encode(cm.getValue()).length > 262144) throw new Error('Select a smaller file for indentation (256 KiB limit).');
            if (cm.somethingSelected()) cm.indentSelection('smart');
            else {
              const pos = cm.getCursor();
              cm.operation(() => { for(let n=0;n<cm.lineCount();n++) cm.indentLine(n,'smart'); cm.setCursor(pos); });
            }
            message('Indentation updated.');
          }
        } catch(error) { message(error.message || 'Cannot format file.',true); }
      }
    });
    form.addEventListener('submit', event => {
      if (encoder.encode(value()).length > max) {
        event.preventDefault(); message('File exceeds the 2 MiB editor limit.',true); return;
      }
      cm.save();
      submitting = true;
    });
    // Native HTML form encoding normalizes line endings. Encode UTF-8 bytes
    // in the formdata event so the server receives exactly the editor value.
    form.addEventListener('formdata', event => {
      event.formData.set('content', encodeBase64(value()));
      event.formData.set('content_encoding','base64');
    });
    form.dataset.editorReady = '1';
    tools.hidden = false;
    cm.getInputField().setAttribute('aria-label','Source code');
    cm.getWrapperElement().setAttribute('aria-label','Code editor');
    active = { form, dirty, submitting: () => submitting, cleanup: () => {
      clearTimeout(timer); cm.toTextArea();
    }};
    update();
    if (source.dataset.eol === 'mixed') message('Mixed line endings will be normalized to LF on save.');
    requestAnimationFrame(() => cm.refresh());
  }
  window.ogpInitCodeEditors = () => {
    const form = document.querySelector('form[data-code-editor]');
    if (form) initialize(form);
  };
  addEventListener('beforeunload', event => {
    if (active?.form.isConnected && active.dirty() && !active.submitting()) {
      event.preventDefault(); event.returnValue = '';
    }
  });
  document.addEventListener('htmx:beforeRequest', event => {
    if (active?.form.isConnected && active.dirty() && !active.submitting() &&
        event.detail.target === document.body && !confirm('Discard unsaved file changes?')) event.preventDefault();
  });
  document.addEventListener('htmx:beforeCleanupElement', event => {
    if (active && (event.detail.elt === active.form || event.detail.elt.contains(active.form))) {
      active.cleanup(); active = null;
    }
  });
})();