/*
 * Hermod chat widget.
 *
 *   <script src="https://HERMOD/api/chat/widget.js" async
 *           data-endpoint="https://HERMOD/api/chat/support"
 *           data-widget-key="PUBLIC_WIDGET_KEY"
 *           data-title="Support"></script>
 *
 * The widget key is public. The chat source accepts it only from the origins
 * on its allow-list, and it is not a Hermod credential.
 *
 * It uses no eval, no inline style attributes or <style> elements, and writes
 * answers as text, never HTML, so it runs under a strict Content-Security-Policy:
 * the page needs script-src for this file and connect-src for the endpoint.
 */
(function () {
  'use strict';

  var script = document.currentScript;
  if (!script) return;
  var endpoint = script.getAttribute('data-endpoint');
  var widgetKey = script.getAttribute('data-widget-key');
  if (!endpoint || !widgetKey) {
    if (window.console) window.console.error('Hermod chat: data-endpoint and data-widget-key are required');
    return;
  }
  var title = script.getAttribute('data-title') || 'Chat';
  var url = endpoint + (endpoint.indexOf('?') < 0 ? '?' : '&') + 'widget_key=' + encodeURIComponent(widgetKey);
  var storageKey = 'hermod-chat:' + endpoint;

  // The conversation lasts as long as the tab, so the workflow's memory
  // follows the visitor from page to page of the site.
  var conversation = null;
  try { conversation = window.sessionStorage.getItem(storageKey); } catch (e) { /* storage blocked */ }

  function el(tag, styles, text) {
    var node = document.createElement(tag);
    for (var k in styles) {
      if (Object.prototype.hasOwnProperty.call(styles, k)) node.style[k] = styles[k];
    }
    if (text) node.textContent = text;
    return node;
  }

  var font = '14px/1.4 system-ui, -apple-system, "Segoe UI", sans-serif';
  var toggle = el('button', {
    position: 'fixed', right: '20px', bottom: '20px', zIndex: '2147483000', padding: '10px 16px',
    border: 'none', borderRadius: '24px', background: '#1c7ed6', color: '#fff', font: font, cursor: 'pointer',
    boxShadow: '0 2px 8px rgba(0,0,0,.2)'
  }, title);
  toggle.type = 'button';
  toggle.setAttribute('aria-expanded', 'false');

  var panel = el('div', {
    position: 'fixed', right: '20px', bottom: '72px', zIndex: '2147483000', width: '340px',
    maxWidth: 'calc(100vw - 40px)', height: '440px', maxHeight: 'calc(100vh - 100px)', display: 'none',
    flexDirection: 'column', background: '#fff', color: '#212529', border: '1px solid #dee2e6',
    borderRadius: '8px', boxShadow: '0 4px 16px rgba(0,0,0,.15)', font: font, overflow: 'hidden'
  });
  panel.setAttribute('role', 'dialog');
  panel.setAttribute('aria-label', title);

  var header = el('div', { padding: '10px 12px', fontWeight: '600', borderBottom: '1px solid #dee2e6' }, title);
  var log = el('div', { flex: '1', overflowY: 'auto', padding: '8px 12px' });
  log.setAttribute('aria-live', 'polite');
  var form = el('form', { display: 'flex', borderTop: '1px solid #dee2e6', margin: '0' });
  var input = el('input', { flex: '1', minWidth: '0', border: 'none', padding: '10px 12px', font: font, outline: 'none' });
  input.type = 'text';
  input.maxLength = 4000;
  input.placeholder = 'Type a message';
  input.setAttribute('aria-label', 'Message');
  var send = el('button', {
    border: 'none', background: 'transparent', color: '#1c7ed6', padding: '0 14px', font: font,
    fontWeight: '600', cursor: 'pointer'
  }, 'Send');
  send.type = 'submit';

  form.appendChild(input);
  form.appendChild(send);
  panel.appendChild(header);
  panel.appendChild(log);
  panel.appendChild(form);

  function say(text, mine) {
    var bubble = el('div', {
      margin: '6px 0', padding: '8px 10px', borderRadius: '8px', maxWidth: '85%', width: 'fit-content',
      whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', background: mine ? '#e7f5ff' : '#f1f3f5',
      marginLeft: mine ? 'auto' : '0'
    }, text);
    log.appendChild(bubble);
    log.scrollTop = log.scrollHeight;
  }

  function remember(id) {
    if (!id) return;
    conversation = id;
    try { window.sessionStorage.setItem(storageKey, id); } catch (e) { /* storage blocked */ }
  }

  toggle.addEventListener('click', function () {
    var open = panel.style.display === 'none';
    panel.style.display = open ? 'flex' : 'none';
    toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    if (open) input.focus();
  });

  form.addEventListener('submit', function (ev) {
    ev.preventDefault();
    var text = input.value.trim();
    if (!text || send.disabled) return;
    input.value = '';
    say(text, true);
    send.disabled = true;
    var body = { message: text };
    if (conversation) body.conversation_id = conversation;
    // text/plain keeps this a simple request: no preflight, and so nothing
    // for the server to answer before the message itself.
    fetch(url, {
      method: 'POST',
      mode: 'cors',
      credentials: 'omit',
      headers: { 'Content-Type': 'text/plain' },
      body: JSON.stringify(body)
    }).then(function (res) {
      return res.json().then(function (data) { return { status: res.status, ok: res.ok, data: data || {} }; });
    }).then(function (r) {
      remember(r.data.conversation_id);
      if (r.status === 202) say('Still working on that. Please ask again in a moment.', false);
      else if (r.ok) say(r.data.reply || '(no answer)', false);
      else say(r.data.error || 'Something went wrong.', false);
    }).catch(function () {
      say('The chat could not be reached. Please try again.', false);
    }).then(function () {
      send.disabled = false;
      input.focus();
    });
  });

  function mount() {
    document.body.appendChild(panel);
    document.body.appendChild(toggle);
  }
  if (document.body) mount();
  else document.addEventListener('DOMContentLoaded', mount);
})();
