// Portable HTML reading only. All dependencies are included in this ZIP.
document.addEventListener('DOMContentLoaded', async () => {
  for (const node of document.querySelectorAll('.language-math')) {
    try { katex.render(node.textContent, node, {displayMode: node.tagName === 'DIV', throwOnError: false, trust: false, strict: 'warn'}); }
    catch (error) { node.dataset.renderError = String(error); }
  }
  mermaid.initialize({startOnLoad: false, securityLevel: 'strict', suppressErrorRendering: true});
  for (const node of document.querySelectorAll('.language-mermaid')) {
    node.textContent = node.textContent.replace(/%%\{[\s\S]*?\}%%/g, '');
    node.classList.add('mermaid');
    try { await mermaid.run({nodes: [node], suppressErrors: true}); }
    catch (error) { node.dataset.renderError = String(error); }
  }
});
