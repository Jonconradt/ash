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

  document.querySelectorAll('.copy-button').forEach(function (button) {
    button.addEventListener('click', function () {
      var target = document.querySelector(button.getAttribute('data-copy-target'));
      if (!target) return;
      navigator.clipboard.writeText(target.textContent.trim()).then(function () {
        var original = button.textContent;
        button.textContent = button.getAttribute('data-copied-label');
        button.classList.add('copied');
        setTimeout(function () {
          button.textContent = original;
          button.classList.remove('copied');
        }, 1800);
      });
    });
  });
})();
