(function () {
  var script = document.querySelector('[data-site-script]');
  var picker = document.querySelector('#site-language');

  if (script && picker) {
    var siteRoot = new URL('.', script.src);
    var path = window.location.pathname.slice(siteRoot.pathname.length);
    var segments = path.split('/');
    var localePaths = Array.prototype.map.call(picker.options, function (option) {
      return option.value;
    });

    if (localePaths.indexOf(segments[0]) !== -1) {
      segments.shift();
    }

    var page = segments.join('/') || 'index.html';
    var currentLocale = window.location.pathname.slice(siteRoot.pathname.length).split('/')[0];
    picker.value = localePaths.indexOf(currentLocale) === -1 ? 'en' : currentLocale;

    picker.addEventListener('change', function () {
      var target = picker.value === 'en'
        ? new URL(page, siteRoot)
        : new URL(picker.value + '/' + page, siteRoot);
      target.search = window.location.search;
      target.hash = window.location.hash;
      window.location.assign(target.href);
    });
  }

  var guide = document.querySelector('.install-guide .docs-content');
  if (guide) {
    guide.querySelectorAll('pre').forEach(function (block, index) {
      var wrapper = document.createElement('div');
      wrapper.className = 'code-block-wrap';
      block.parentNode.insertBefore(wrapper, block);
      wrapper.appendChild(block);
      block.id = 'install-command-' + index;
      var button = document.createElement('button');
      button.type = 'button';
      button.className = 'copy-button';
      button.textContent = guide.getAttribute('data-copy-label');
      button.setAttribute('data-copy-target', '#' + block.id);
      button.setAttribute('data-copied-label', guide.getAttribute('data-copied-label'));
      button.setAttribute('data-copy-error', guide.getAttribute('data-copy-error'));
      wrapper.appendChild(button);
      var status = document.createElement('p');
      status.className = 'copy-status';
      status.setAttribute('role', 'status');
      wrapper.appendChild(status);
    });
  }

  document.querySelectorAll('.copy-button').forEach(function (button) {
    button.addEventListener('click', function () {
      var target = document.querySelector(button.getAttribute('data-copy-target'));
      if (!target) return;
      var status = button.parentNode.querySelector('.copy-status');
      function failed() {
        if (status) status.textContent = button.getAttribute('data-copy-error');
      }
      if (status) status.textContent = '';
      if (!navigator.clipboard || !navigator.clipboard.writeText) {
        failed();
        return;
      }
      navigator.clipboard.writeText(target.textContent.trim()).then(function () {
        var original = button.textContent;
        if (status) status.textContent = button.getAttribute('data-copied-label');
        button.textContent = button.getAttribute('data-copied-label');
        button.classList.add('copied');
        button.disabled = true;
        setTimeout(function () {
          button.textContent = original;
          button.classList.remove('copied');
          button.disabled = false;
        }, 1800);
      }, failed);
    });
  });
})();
