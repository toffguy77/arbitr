#!/bin/bash

echo "🚀 ЗАПУСК АГРЕССИВНОЙ АРБИТРАЖНОЙ СИСТЕМЫ 🚀"
echo "=============================================="

# Проверяем API ключи
if [ -z "$ARBITR_BYBIT_API_KEY" ] || [ "$ARBITR_BYBIT_API_KEY" = "" ]; then
    echo "❌ КРИТИЧЕСКАЯ ОШИБКА: API ключи Bybit не установлены!"
    echo "📝 Установите реальные API ключи в .env файле:"
    echo "   ARBITR_BYBIT_API_KEY=ваш_api_ключ"
    echo "   ARBITR_BYBIT_SECRET=ваш_секрет"
    echo ""
    echo "🔗 Получить API ключи: https://www.bybit.com/app/user/api-management"
    echo "⚠️  ВНИМАНИЕ: Включите spot trading permissions!"
    exit 1
fi

echo "✅ API ключи найдены"
echo "🎯 Режим: АГРЕССИВНАЯ ТОРГОВЛЯ"
echo "💰 Цель: МАКСИМАЛЬНЫЙ ЗАРАБОТОК"
echo ""

# Компилируем
echo "🔨 Компиляция..."
go build -o arbitr ./cmd/arbitrage
if [ $? -ne 0 ]; then
    echo "❌ Ошибка компиляции!"
    exit 1
fi

echo "✅ Компиляция завершена"
echo ""

# Показываем настройки
echo "⚙️  АГРЕССИВНЫЕ НАСТРОЙКИ:"
echo "   • Минимальная прибыль: 3 bps (снижено с 5)"
echo "   • Максимальный размер: $2000 (увеличено с $500)"
echo "   • Ордеров в минуту: 500 (увеличено с 100)"
echo "   • Кулдауны: ОТКЛЮЧЕНЫ"
echo "   • Подтверждения: ОТКЛЮЧЕНЫ"
echo "   • Параллельных треугольников: 5"
echo ""

# Запускаем
echo "🚀 ЗАПУСК ТОРГОВОЙ СИСТЕМЫ..."
echo "📊 Мониторинг: http://localhost:19091/metrics"
echo "❤️  Здоровье: http://localhost:19091/healthz"
echo ""
echo "💡 Для остановки нажмите Ctrl+C"
echo "=============================================="

./arbitr